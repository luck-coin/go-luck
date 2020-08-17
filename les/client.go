// Copyright 2020 The go-luck Authors
// This file is part of the go-luck library.
//
// The go-luck library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-luck library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-luck library. If not, see <http://www.gnu.org/licenses/>.

// Package les implements the Light Luck Subprotocol.
package les

import (
	"fmt"
	"time"

	"github.com/luck/go-luck/accounts"
	"github.com/luck/go-luck/accounts/abi/bind"
	"github.com/luck/go-luck/common"
	"github.com/luck/go-luck/common/hexutil"
	"github.com/luck/go-luck/common/mclock"
	"github.com/luck/go-luck/consensus"
	"github.com/luck/go-luck/core"
	"github.com/luck/go-luck/core/bloombits"
	"github.com/luck/go-luck/core/rawdb"
	"github.com/luck/go-luck/core/types"
	"github.com/luck/go-luck/fort"
	"github.com/luck/go-luck/fort/downloader"
	"github.com/luck/go-luck/fort/filters"
	"github.com/luck/go-luck/fort/gasprice"
	"github.com/luck/go-luck/event"
	"github.com/luck/go-luck/internal/fortapi"
	"github.com/luck/go-luck/les/checkpointoracle"
	lpc "github.com/luck/go-luck/les/lespay/client"
	"github.com/luck/go-luck/light"
	"github.com/luck/go-luck/log"
	"github.com/luck/go-luck/node"
	"github.com/luck/go-luck/p2p"
	"github.com/luck/go-luck/p2p/lnode"
	"github.com/luck/go-luck/params"
	"github.com/luck/go-luck/rpc"
)

type LightLuck struct {
	lesCommons

	peers        *serverPeerSet
	reqDist      *requestDistributor
	retriever    *retrieveManager
	odr          *LesOdr
	relay        *lesTxRelay
	handler      *clientHandler
	txPool       *light.TxPool
	blockchain   *light.LightChain
	serverPool   *serverPool
	valueTracker *lpc.ValueTracker

	bloomRequests chan chan *bloombits.Retrieval // Channel receiving bloom data retrieval requests
	bloomIndexer  *core.ChainIndexer             // Bloom indexer operating during block imports

	ApiBackend     *LesApiBackend
	eventMux       *event.TypeMux
	engine         consensus.Engine
	accountManager *accounts.Manager
	netRPCService  *fortapi.PublicNetAPI
}

func New(ctx *node.ServiceContext, config *fort.Config) (*LightLuck, error) {
	chainDb, err := ctx.OpenDatabase("lightchaindata", config.DatabaseCache, config.DatabaseHandles, "fort/db/chaindata/")
	if err != nil {
		return nil, err
	}
	lespayDb, err := ctx.OpenDatabase("lespay", 0, 0, "fort/db/lespay")
	if err != nil {
		return nil, err
	}
	chainConfig, genesisHash, genesisErr := core.SetupGenesisBlockWithOverride(chainDb, config.Genesis,
		config.OverrideIstanbul, config.OverrideMuirGlacier)
	if _, isCompat := genesisErr.(*params.ConfigCompatError); genesisErr != nil && !isCompat {
		return nil, genesisErr
	}
	//log.Info("Initialised chain configuration", "config", chainConfig)

	peers := newServerPeerSet()
	lfort := &LightLuck{
		lesCommons: lesCommons{
			genesis:     genesisHash,
			config:      config,
			chainConfig: chainConfig,
			iConfig:     light.DefaultClientIndexerConfig,
			chainDb:     chainDb,
			closeCh:     make(chan struct{}),
		},
		peers:          peers,
		eventMux:       ctx.EventMux,
		reqDist:        newRequestDistributor(peers, &mclock.System{}),
		accountManager: ctx.AccountManager,
		engine:         fort.CreateConsensusEngine(ctx, chainConfig, &config.Ethash, nil, false, chainDb),
		bloomRequests:  make(chan chan *bloombits.Retrieval),
		bloomIndexer:   fort.NewBloomIndexer(chainDb, params.BloomBitsBlocksClient, params.HelperTrieConfirmations),
		serverPool:     newServerPool(chainDb, config.UltraLightServers),
		valueTracker:   lpc.NewValueTracker(lespayDb, &mclock.System{}, requestList, time.Minute, 1/float64(time.Hour), 1/float64(time.Hour*100), 1/float64(time.Hour*1000)),
	}
	peers.subscribe((*vtSubscription)(lfort.valueTracker))
	lfort.retriever = newRetrieveManager(peers, lfort.reqDist, lfort.serverPool)
	lfort.relay = newLesTxRelay(peers, lfort.retriever)

	lfort.odr = NewLesOdr(chainDb, light.DefaultClientIndexerConfig, lfort.retriever)
	lfort.chtIndexer = light.NewChtIndexer(chainDb, lfort.odr, params.CHTFrequency, params.HelperTrieConfirmations)
	lfort.bloomTrieIndexer = light.NewBloomTrieIndexer(chainDb, lfort.odr, params.BloomBitsBlocksClient, params.BloomTrieFrequency)
	lfort.odr.SetIndexers(lfort.chtIndexer, lfort.bloomTrieIndexer, lfort.bloomIndexer)

	checkpoint := config.Checkpoint
	if checkpoint == nil {
		checkpoint = params.TrustedCheckpoints[genesisHash]
	}
	// Note: NewLightChain adds the trusted checkpoint so it needs an ODR with
	// indexers already set but not started yet
	if lfort.blockchain, err = light.NewLightChain(lfort.odr, lfort.chainConfig, lfort.engine, checkpoint); err != nil {
		return nil, err
	}
	lfort.chainReader = lfort.blockchain
	lfort.txPool = light.NewTxPool(lfort.chainConfig, lfort.blockchain, lfort.relay)

	// Set up checkpoint oracle.
	oracle := config.CheckpointOracle
	if oracle == nil {
		oracle = params.CheckpointOracles[genesisHash]
	}
	lfort.oracle = checkpointoracle.New(oracle, lfort.localCheckpoint)

	// Note: AddChildIndexer starts the update process for the child
	lfort.bloomIndexer.AddChildIndexer(lfort.bloomTrieIndexer)
	lfort.chtIndexer.Start(lfort.blockchain)
	lfort.bloomIndexer.Start(lfort.blockchain)

	lfort.handler = newClientHandler(config.UltraLightServers, config.UltraLightFraction, checkpoint, lfort)
	if lfort.handler.ulc != nil {
		log.Warn("Ultra light client is enabled", "trustedNodes", len(lfort.handler.ulc.keys), "minTrustedFraction", lfort.handler.ulc.fraction)
		lfort.blockchain.DisableCheckFreq()
	}
	// Rewind the chain in case of an incompatible config upgrade.
	if compat, ok := genesisErr.(*params.ConfigCompatError); ok {
		log.Warn("Rewinding chain to upgrade configuration", "err", compat)
		lfort.blockchain.SetHead(compat.RewindTo)
		rawdb.WriteChainConfig(chainDb, genesisHash, chainConfig)
	}

	lfort.ApiBackend = &LesApiBackend{ctx.ExtRPCEnabled(), lfort, nil}
	gpoParams := config.GPO
	if gpoParams.Default == nil {
		gpoParams.Default = config.Miner.GasPrice
	}
	lfort.ApiBackend.gpo = gasprice.NewOracle(lfort.ApiBackend, gpoParams)

	return lfort, nil
}

// vtSubscription implements serverPeerSubscriber
type vtSubscription lpc.ValueTracker

// registerPeer implements serverPeerSubscriber
func (v *vtSubscription) registerPeer(p *serverPeer) {
	vt := (*lpc.ValueTracker)(v)
	p.setValueTracker(vt, vt.Register(p.ID()))
	p.updateVtParams()
}

// unregisterPeer implements serverPeerSubscriber
func (v *vtSubscription) unregisterPeer(p *serverPeer) {
	vt := (*lpc.ValueTracker)(v)
	vt.Unregister(p.ID())
	p.setValueTracker(nil, nil)
}

type LightDummyAPI struct{}

// Etherbase is the address that mining rewards will be send to
func (s *LightDummyAPI) Etherbase() (common.Address, error) {
	return common.Address{}, fmt.Errorf("mining is not supported in light mode")
}

// Coinbase is the address that mining rewards will be send to (alias for Etherbase)
func (s *LightDummyAPI) Coinbase() (common.Address, error) {
	return common.Address{}, fmt.Errorf("mining is not supported in light mode")
}

// Hashrate returns the POW hashrate
func (s *LightDummyAPI) Hashrate() hexutil.Uint {
	return 0
}

// Mining returns an indication if this node is currently mining.
func (s *LightDummyAPI) Mining() bool {
	return false
}

// APIs returns the collection of RPC services the luck package offers.
// NOTE, some of these services probably need to be moved to somewhere else.
func (s *LightLuck) APIs() []rpc.API {
	apis := fortapi.GetAPIs(s.ApiBackend)
	apis = append(apis, s.engine.APIs(s.BlockChain().HeaderChain())...)
	return append(apis, []rpc.API{
		{
			Namespace: "fort",
			Version:   "1.0",
			Service:   &LightDummyAPI{},
			Public:    true,
		}, {
			Namespace: "fort",
			Version:   "1.0",
			Service:   downloader.NewPublicDownloaderAPI(s.handler.downloader, s.eventMux),
			Public:    true,
		}, {
			Namespace: "fort",
			Version:   "1.0",
			Service:   filters.NewPublicFilterAPI(s.ApiBackend, true),
			Public:    true,
		}, {
			Namespace: "net",
			Version:   "1.0",
			Service:   s.netRPCService,
			Public:    true,
		}, {
			Namespace: "les",
			Version:   "1.0",
			Service:   NewPrivateLightAPI(&s.lesCommons),
			Public:    false,
		}, {
			Namespace: "lespay",
			Version:   "1.0",
			Service:   lpc.NewPrivateClientAPI(s.valueTracker),
			Public:    false,
		},
	}...)
}

func (s *LightLuck) ResetWithGenesisBlock(gb *types.Block) {
	s.blockchain.ResetWithGenesisBlock(gb)
}

func (s *LightLuck) BlockChain() *light.LightChain      { return s.blockchain }
func (s *LightLuck) TxPool() *light.TxPool              { return s.txPool }
func (s *LightLuck) Engine() consensus.Engine           { return s.engine }
func (s *LightLuck) LesVersion() int                    { return int(ClientProtocolVersions[0]) }
func (s *LightLuck) Downloader() *downloader.Downloader { return s.handler.downloader }
func (s *LightLuck) EventMux() *event.TypeMux           { return s.eventMux }

// Protocols implements node.Service, returning all the currently configured
// network protocols to start.
func (s *LightLuck) Protocols() []p2p.Protocol {
	return s.makeProtocols(ClientProtocolVersions, s.handler.runPeer, func(id lnode.ID) interface{} {
		if p := s.peers.peer(peerIdToString(id)); p != nil {
			return p.Info()
		}
		return nil
	})
}

// Start implements node.Service, starting all internal goroutines needed by the
// light luck protocol implementation.
func (s *LightLuck) Start(srvr *p2p.Server) error {
	log.Warn("Light client mode is an experimental feature")

	// Start bloom request workers.
	s.wg.Add(bloomServiceThreads)
	s.startBloomHandlers(params.BloomBitsBlocksClient)

	s.netRPCService = fortapi.NewPublicNetAPI(srvr, s.config.NetworkId)

	// clients are searching for the first advertised protocol in the list
	protocolVersion := AdvertiseProtocolVersions[0]
	s.serverPool.start(srvr, lesTopic(s.blockchain.Genesis().Hash(), protocolVersion))
	return nil
}

// Stop implements node.Service, terminating all internal goroutines used by the
// Luck protocol.
func (s *LightLuck) Stop() error {
	close(s.closeCh)
	s.peers.close()
	s.reqDist.close()
	s.odr.Stop()
	s.relay.Stop()
	s.bloomIndexer.Close()
	s.chtIndexer.Close()
	s.blockchain.Stop()
	s.handler.stop()
	s.txPool.Stop()
	s.engine.Close()
	s.eventMux.Stop()
	s.serverPool.stop()
	s.valueTracker.Stop()
	s.chainDb.Close()
	s.wg.Wait()
	log.Info("Light luck stopped")
	return nil
}

// SetClient sets the rpc client and binds the registrar contract.
func (s *LightLuck) SetContractBackend(backend bind.ContractBackend) {
	if s.oracle == nil {
		return
	}
	s.oracle.Start(backend)
}

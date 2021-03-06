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

// Package fort implements the Luck protocol.
package fort

import (
	"errors"
	"fmt"
	"math/big"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/luck/go-luck/accounts"
	"github.com/luck/go-luck/accounts/abi/bind"
	"github.com/luck/go-luck/common"
	"github.com/luck/go-luck/common/hexutil"
	"github.com/luck/go-luck/consensus"
	"github.com/luck/go-luck/consensus/clique"
	"github.com/luck/go-luck/consensus/ethash"
	"github.com/luck/go-luck/consensus/tppow"
	"github.com/luck/go-luck/core"
	"github.com/luck/go-luck/core/bloombits"
	"github.com/luck/go-luck/core/rawdb"
	"github.com/luck/go-luck/core/types"
	"github.com/luck/go-luck/core/vm"
	"github.com/luck/go-luck/fort/downloader"
	"github.com/luck/go-luck/fort/filters"
	"github.com/luck/go-luck/fort/gasprice"
	"github.com/luck/go-luck/fortdb"
	"github.com/luck/go-luck/event"
	"github.com/luck/go-luck/internal/fortapi"
	"github.com/luck/go-luck/log"
	"github.com/luck/go-luck/miner"
	"github.com/luck/go-luck/node"
	"github.com/luck/go-luck/p2p"
	"github.com/luck/go-luck/p2p/lnode"
	"github.com/luck/go-luck/p2p/enr"
	"github.com/luck/go-luck/params"
	"github.com/luck/go-luck/rlp"
	"github.com/luck/go-luck/rpc"
)

type LesServer interface {
	Start(srvr *p2p.Server)
	Stop()
	APIs() []rpc.API
	Protocols() []p2p.Protocol
	SetBloomBitsIndexer(bbIndexer *core.ChainIndexer)
	SetContractBackend(bind.ContractBackend)
}

// Luck implements the Luck full node service.
type Luck struct {
	config *Config

	// Handlers
	txPool          *core.TxPool
	blockchain      *core.BlockChain
	protocolManager *ProtocolManager
	lesServer       LesServer
	dialCandiates   lnode.Iterator

	// DB interfaces
	chainDb fortdb.Database // Block chain database

	eventMux       *event.TypeMux
	engine         consensus.Engine
	accountManager *accounts.Manager

	bloomRequests     chan chan *bloombits.Retrieval // Channel receiving bloom data retrieval requests
	bloomIndexer      *core.ChainIndexer             // Bloom indexer operating during block imports
	closeBloomHandler chan struct{}

	APIBackend *EthAPIBackend

	miner     *miner.Miner
	gasPrice  *big.Int
	forterbase common.Address

	networkID     uint64
	netRPCService *fortapi.PublicNetAPI

	lock sync.RWMutex // Protects the variadic fields (e.g. gas price and forterbase)
}

func (s *Luck) AddLesServer(ls LesServer) {
	s.lesServer = ls
	ls.SetBloomBitsIndexer(s.bloomIndexer)
}

// SetClient sets a rpc client which connecting to our local node.
func (s *Luck) SetContractBackend(backend bind.ContractBackend) {
	// Pass the rpc client to les server if it is enabled.
	if s.lesServer != nil {
		s.lesServer.SetContractBackend(backend)
	}
}

// New creates a new Luck object (including the
// initialisation of the common Luck object)
func New(ctx *node.ServiceContext, config *Config) (*Luck, error) {
	// Ensure configuration values are compatible and sane
	if config.SyncMode == downloader.LightSync {
		return nil, errors.New("can't run fort.Luck in light sync mode, use les.LightLuck")
	}
	if !config.SyncMode.IsValid() {
		return nil, fmt.Errorf("invalid sync mode %d", config.SyncMode)
	}
	if config.Miner.GasPrice == nil || config.Miner.GasPrice.Cmp(common.Big0) <= 0 {
		log.Warn("Sanitizing invalid miner gas price", "provided", config.Miner.GasPrice, "updated", DefaultConfig.Miner.GasPrice)
		config.Miner.GasPrice = new(big.Int).Set(DefaultConfig.Miner.GasPrice)
	}
	if config.NoPruning && config.TrieDirtyCache > 0 {
		config.TrieCleanCache += config.TrieDirtyCache * 3 / 5
		config.SnapshotCache += config.TrieDirtyCache * 3 / 5
		config.TrieDirtyCache = 0
	}
	log.Info("Allocated trie memory caches", "clean", common.StorageSize(config.TrieCleanCache)*1024*1024, "dirty", common.StorageSize(config.TrieDirtyCache)*1024*1024)

	// Assemble the Luck object
	chainDb, err := ctx.OpenDatabaseWithFreezer("chaindata", config.DatabaseCache, config.DatabaseHandles, config.DatabaseFreezer, "fort/db/chaindata/")
	if err != nil {
		return nil, err
	}
	chainConfig, genesisHash, genesisErr := core.SetupGenesisBlockWithOverride(chainDb, config.Genesis, config.OverrideIstanbul, config.OverrideMuirGlacier)
	if _, ok := genesisErr.(*params.ConfigCompatError); genesisErr != nil && !ok {
		return nil, genesisErr
	}
	//log.Info("Initialised chain configuration", "config", chainConfig)

	fort := &Luck{
		config:            config,
		chainDb:           chainDb,
		eventMux:          ctx.EventMux,
		accountManager:    ctx.AccountManager,
		engine:            CreateConsensusEngine(ctx, chainConfig, &config.Ethash, config.Miner.Notify, config.Miner.Noverify, chainDb),
		closeBloomHandler: make(chan struct{}),
		networkID:         config.NetworkId,
		gasPrice:          config.Miner.GasPrice,
		forterbase:         config.Miner.Etherbase,
		bloomRequests:     make(chan chan *bloombits.Retrieval),
		bloomIndexer:      NewBloomIndexer(chainDb, params.BloomBitsBlocks, params.BloomConfirms),
	}

	bcVersion := rawdb.ReadDatabaseVersion(chainDb)
	var dbVer = "<nil>"
	if bcVersion != nil {
		dbVer = fmt.Sprintf("%d", *bcVersion)
	}
	log.Info("Initialising Luck protocol", "versions", ProtocolVersions, "network", config.NetworkId, "dbversion", dbVer)

	if !config.SkipBcVersionCheck {
		if bcVersion != nil && *bcVersion > core.BlockChainVersion {
			return nil, fmt.Errorf("database version is v%d, Luck %s only supports v%d", *bcVersion, params.VersionWithMeta, core.BlockChainVersion)
		} else if bcVersion == nil || *bcVersion < core.BlockChainVersion {
			log.Warn("Upgrade blockchain database version", "from", dbVer, "to", core.BlockChainVersion)
			rawdb.WriteDatabaseVersion(chainDb, core.BlockChainVersion)
		}
	}
	var (
		vmConfig = vm.Config{
			EnablePreimageRecording: config.EnablePreimageRecording,
			EWASMInterpreter:        config.EWASMInterpreter,
			EVMInterpreter:          config.EVMInterpreter,
		}
		cacheConfig = &core.CacheConfig{
			TrieCleanLimit:      config.TrieCleanCache,
			TrieCleanNoPrefetch: config.NoPrefetch,
			TrieDirtyLimit:      config.TrieDirtyCache,
			TrieDirtyDisabled:   config.NoPruning,
			TrieTimeLimit:       config.TrieTimeout,
			SnapshotLimit:       config.SnapshotCache,
		}
	)
	fort.blockchain, err = core.NewBlockChain(chainDb, cacheConfig, chainConfig, fort.engine, vmConfig, fort.shouldPreserve)
	if err != nil {
		return nil, err
	}
	// Rewind the chain in case of an incompatible config upgrade.
	if compat, ok := genesisErr.(*params.ConfigCompatError); ok {
		log.Warn("Rewinding chain to upgrade configuration", "err", compat)
		fort.blockchain.SetHead(compat.RewindTo)
		rawdb.WriteChainConfig(chainDb, genesisHash, chainConfig)
	}
	fort.bloomIndexer.Start(fort.blockchain)

	if config.TxPool.Journal != "" {
		config.TxPool.Journal = ctx.ResolvePath(config.TxPool.Journal)
	}
	fort.txPool = core.NewTxPool(config.TxPool, chainConfig, fort.blockchain)

	// Permit the downloader to use the trie cache allowance during fast sync
	cacheLimit := cacheConfig.TrieCleanLimit + cacheConfig.TrieDirtyLimit + cacheConfig.SnapshotLimit
	checkpoint := config.Checkpoint
	if checkpoint == nil {
		checkpoint = params.TrustedCheckpoints[genesisHash]
	}
	if fort.protocolManager, err = NewProtocolManager(chainConfig, checkpoint, config.SyncMode, config.NetworkId, fort.eventMux, fort.txPool, fort.engine, fort.blockchain, chainDb, cacheLimit, config.Whitelist); err != nil {
		return nil, err
	}
	fort.miner = miner.New(fort, &config.Miner, chainConfig, fort.EventMux(), fort.engine, fort.isLocalBlock)
	fort.miner.SetExtra(makeExtraData(config.Miner.ExtraData))

	fort.APIBackend = &EthAPIBackend{ctx.ExtRPCEnabled(), fort, nil}
	gpoParams := config.GPO
	if gpoParams.Default == nil {
		gpoParams.Default = config.Miner.GasPrice
	}
	fort.APIBackend.gpo = gasprice.NewOracle(fort.APIBackend, gpoParams)

	fort.dialCandiates, err = fort.setupDiscovery(&ctx.Config.P2P)
	if err != nil {
		return nil, err
	}

	return fort, nil
}

func makeExtraData(extra []byte) []byte {
	if len(extra) == 0 {
		// create default extradata
		extra, _ = rlp.EncodeToBytes([]interface{}{
			uint(params.VersionMajor<<16 | params.VersionMinor<<8 | params.VersionPatch),
			"luck",
			runtime.Version(),
			runtime.GOOS,
		})
	}
	if uint64(len(extra)) > params.MaximumExtraDataSize {
		log.Warn("Miner extra data exceed limit", "extra", hexutil.Bytes(extra), "limit", params.MaximumExtraDataSize)
		extra = nil
	}
	return extra
}

// CreateConsensusEngine creates the required type of consensus engine instance for an Luck service
func CreateConsensusEngine(ctx *node.ServiceContext, chainConfig *params.ChainConfig, config *ethash.Config, notify []string, noverify bool, db fortdb.Database) consensus.Engine {
	// If proof-of-authority is requested, set it up
	// if chainConfig.Clique != nil {
	// 	return clique.New(chainConfig.Clique, db)
	// }
	// // Otherwise assume proof-of-work
	// switch config.PowMode {
	// case ethash.ModeFake:
	// 	log.Warn("Ethash used in fake mode")
	// 	return ethash.NewFaker()
	// case ethash.ModeTest:
	// 	log.Warn("Ethash used in test mode")
	// 	return ethash.NewTester(nil, noverify)
	// case ethash.ModeShared:
	// 	log.Warn("Ethash used in shared mode")
	// 	return ethash.NewShared()
	// default:
	// 	engine := ethash.New(ethash.Config{
	// 		CacheDir:         ctx.ResolvePath(config.CacheDir),
	// 		CachesInMem:      config.CachesInMem,
	// 		CachesOnDisk:     config.CachesOnDisk,
	// 		CachesLockMmap:   config.CachesLockMmap,
	// 		DatasetDir:       config.DatasetDir,
	// 		DatasetsInMem:    config.DatasetsInMem,
	// 		DatasetsOnDisk:   config.DatasetsOnDisk,
	// 		DatasetsLockMmap: config.DatasetsLockMmap,
	// 	}, notify, noverify)
	// 	engine.SetThreads(-1) // Disable CPU mining
	// 	return engine
	// }

	return tppow.New()
}

// APIs return the collection of RPC services the luck package offers.
// NOTE, some of these services probably need to be moved to somewhere else.
func (s *Luck) APIs() []rpc.API {
	apis := fortapi.GetAPIs(s.APIBackend)

	// Append any APIs exposed explicitly by the les server
	if s.lesServer != nil {
		apis = append(apis, s.lesServer.APIs()...)
	}
	// Append any APIs exposed explicitly by the consensus engine
	apis = append(apis, s.engine.APIs(s.BlockChain())...)

	// Append any APIs exposed explicitly by the les server
	if s.lesServer != nil {
		apis = append(apis, s.lesServer.APIs()...)
	}

	// Append all the local APIs and return
	return append(apis, []rpc.API{
		{
			Namespace: "fort",
			Version:   "1.0",
			Service:   NewPublicLuckAPI(s),
			Public:    true,
		}, {
			Namespace: "fort",
			Version:   "1.0",
			Service:   NewPublicMinerAPI(s),
			Public:    true,
		}, {
			Namespace: "fort",
			Version:   "1.0",
			Service:   downloader.NewPublicDownloaderAPI(s.protocolManager.downloader, s.eventMux),
			Public:    true,
		}, {
			Namespace: "miner",
			Version:   "1.0",
			Service:   NewPrivateMinerAPI(s),
			Public:    false,
		}, {
			Namespace: "fort",
			Version:   "1.0",
			Service:   filters.NewPublicFilterAPI(s.APIBackend, false),
			Public:    true,
		}, {
			Namespace: "admin",
			Version:   "1.0",
			Service:   NewPrivateAdminAPI(s),
		}, {
			Namespace: "debug",
			Version:   "1.0",
			Service:   NewPublicDebugAPI(s),
			Public:    true,
		}, {
			Namespace: "debug",
			Version:   "1.0",
			Service:   NewPrivateDebugAPI(s),
		}, {
			Namespace: "net",
			Version:   "1.0",
			Service:   s.netRPCService,
			Public:    true,
		},
	}...)
}

func (s *Luck) ResetWithGenesisBlock(gb *types.Block) {
	s.blockchain.ResetWithGenesisBlock(gb)
}

func (s *Luck) Etherbase() (eb common.Address, err error) {
	s.lock.RLock()
	forterbase := s.forterbase
	s.lock.RUnlock()

	if forterbase != (common.Address{}) {
		return forterbase, nil
	}
	if wallets := s.AccountManager().Wallets(); len(wallets) > 0 {
		if accounts := wallets[0].Accounts(); len(accounts) > 0 {
			forterbase := accounts[0].Address

			s.lock.Lock()
			s.forterbase = forterbase
			s.lock.Unlock()

			log.Info("Luckbase automatically configured", "address", forterbase)
			return forterbase, nil
		}
	}
	return common.Address{}, fmt.Errorf("luckbase must be explicitly specified")
}

// isLocalBlock checks whforter the specified block is mined
// by local miner accounts.
//
// We regard two types of accounts as local miner account: forterbase
// and accounts specified via `txpool.locals` flag.
func (s *Luck) isLocalBlock(block *types.Block) bool {
	author, err := s.engine.Author(block.Header())
	if err != nil {
		log.Warn("Failed to retrieve block author", "number", block.NumberU64(), "hash", block.Hash(), "err", err)
		return false
	}
	// Check whforter the given address is forterbase.
	s.lock.RLock()
	forterbase := s.forterbase
	s.lock.RUnlock()
	if author == forterbase {
		return true
	}
	// Check whforter the given address is specified by `txpool.local`
	// CLI flag.
	for _, account := range s.config.TxPool.Locals {
		if account == author {
			return true
		}
	}
	return false
}

// shouldPreserve checks whforter we should preserve the given block
// during the chain reorg depending on whforter the author of block
// is a local account.
func (s *Luck) shouldPreserve(block *types.Block) bool {
	// The reason we need to disable the self-reorg preserving for clique
	// is it can be probable to introduce a deadlock.
	//
	// e.g. If there are 7 available signers
	//
	// r1   A
	// r2     B
	// r3       C
	// r4         D
	// r5   A      [X] F G
	// r6    [X]
	//
	// In the round5, the inturn signer E is offline, so the worst case
	// is A, F and G sign the block of round5 and reject the block of opponents
	// and in the round6, the last available signer B is offline, the whole
	// network is stuck.
	if _, ok := s.engine.(*clique.Clique); ok {
		return false
	}
	return s.isLocalBlock(block)
}

// SetEtherbase sets the mining reward address.
func (s *Luck) SetEtherbase(forterbase common.Address) {
	s.lock.Lock()
	s.forterbase = forterbase
	s.lock.Unlock()

	s.miner.SetEtherbase(forterbase)
}

// StartMining starts the miner with the given number of CPU threads. If mining
// is already running, this method adjust the number of threads allowed to use
// and updates the minimum price required by the transaction pool.
func (s *Luck) StartMining(threads int) error {
	// Update the thread count within the consensus engine
	type threaded interface {
		SetThreads(threads int)
	}
	if th, ok := s.engine.(threaded); ok {
		log.Info("Updated mining threads", "threads", threads)
		if threads == 0 {
			threads = -1 // Disable the miner from within
		}
		th.SetThreads(threads)
	}
	// If the miner was not running, initialize it
	if !s.IsMining() {
		// Propagate the initial price point to the transaction pool
		s.lock.RLock()
		price := s.gasPrice
		s.lock.RUnlock()
		s.txPool.SetGasPrice(price)

		// Configure the local mining address
		eb, err := s.Etherbase()
		if err != nil {
			log.Error("Cannot start mining without luckbase", "err", err)
			return fmt.Errorf("luckbase missing: %v", err)
		}
		if clique, ok := s.engine.(*clique.Clique); ok {
			wallet, err := s.accountManager.Find(accounts.Account{Address: eb})
			if wallet == nil || err != nil {
				log.Error("Luckbase account unavailable locally", "err", err)
				return fmt.Errorf("signer missing: %v", err)
			}
			clique.Authorize(eb, wallet.SignData)
		}
		// If mining is started, we can disable the transaction rejection mechanism
		// introduced to speed sync times.
		atomic.StoreUint32(&s.protocolManager.acceptTxs, 1)

		go s.miner.Start(eb)
	}
	return nil
}

// StopMining terminates the miner, both at the consensus engine level as well as
// at the block creation level.
func (s *Luck) StopMining() {
	// Update the thread count within the consensus engine
	type threaded interface {
		SetThreads(threads int)
	}
	if th, ok := s.engine.(threaded); ok {
		th.SetThreads(-1)
	}
	// Stop the block creating itself
	s.miner.Stop()
}

func (s *Luck) IsMining() bool      { return s.miner.Mining() }
func (s *Luck) Miner() *miner.Miner { return s.miner }

func (s *Luck) AccountManager() *accounts.Manager  { return s.accountManager }
func (s *Luck) BlockChain() *core.BlockChain       { return s.blockchain }
func (s *Luck) TxPool() *core.TxPool               { return s.txPool }
func (s *Luck) EventMux() *event.TypeMux           { return s.eventMux }
func (s *Luck) Engine() consensus.Engine           { return s.engine }
func (s *Luck) ChainDb() fortdb.Database            { return s.chainDb }
func (s *Luck) IsListening() bool                  { return true } // Always listening
func (s *Luck) EthVersion() int                    { return int(ProtocolVersions[0]) }
func (s *Luck) NetVersion() uint64                 { return s.networkID }
func (s *Luck) Downloader() *downloader.Downloader { return s.protocolManager.downloader }
func (s *Luck) Synced() bool                       { return atomic.LoadUint32(&s.protocolManager.acceptTxs) == 1 }
func (s *Luck) ArchiveMode() bool                  { return s.config.NoPruning }

// Protocols implements node.Service, returning all the currently configured
// network protocols to start.
func (s *Luck) Protocols() []p2p.Protocol {
	protos := make([]p2p.Protocol, len(ProtocolVersions))
	for i, vsn := range ProtocolVersions {
		protos[i] = s.protocolManager.makeProtocol(vsn)
		protos[i].Attributes = []enr.Entry{s.currentEthEntry()}
		protos[i].DialCandidates = s.dialCandiates
	}
	if s.lesServer != nil {
		protos = append(protos, s.lesServer.Protocols()...)
	}
	return protos
}

// Start implements node.Service, starting all internal goroutines needed by the
// Luck protocol implementation.
func (s *Luck) Start(srvr *p2p.Server) error {
	s.startEthEntryUpdate(srvr.LocalNode())

	// Start the bloom bits servicing goroutines
	s.startBloomHandlers(params.BloomBitsBlocks)

	// Start the RPC service
	s.netRPCService = fortapi.NewPublicNetAPI(srvr, s.NetVersion())

	// Figure out a max peers count based on the server limits
	maxPeers := srvr.MaxPeers
	if s.config.LightServ > 0 {
		if s.config.LightPeers >= srvr.MaxPeers {
			return fmt.Errorf("invalid peer config: light peer count (%d) >= total peer count (%d)", s.config.LightPeers, srvr.MaxPeers)
		}
		maxPeers -= s.config.LightPeers
	}
	// Start the networking layer and the light server if requested
	s.protocolManager.Start(maxPeers)
	if s.lesServer != nil {
		s.lesServer.Start(srvr)
	}
	return nil
}

// Stop implements node.Service, terminating all internal goroutines used by the
// Luck protocol.
func (s *Luck) Stop() error {
	// Stop all the peer-related stuff first.
	s.protocolManager.Stop()
	if s.lesServer != nil {
		s.lesServer.Stop()
	}

	// Then stop everything else.
	s.bloomIndexer.Close()
	close(s.closeBloomHandler)
	s.txPool.Stop()
	s.miner.Stop()
	s.blockchain.Stop()
	s.engine.Close()
	s.chainDb.Close()
	s.eventMux.Stop()
	return nil
}

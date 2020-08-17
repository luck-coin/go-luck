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

// Contains all the wrappers from the node package to support client side node
// management on mobile platforms.

package luck

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/luck/go-luck/core"
	"github.com/luck/go-luck/fort"
	"github.com/luck/go-luck/fort/downloader"
	"github.com/luck/go-luck/fortclient"
	"github.com/luck/go-luck/fortstats"
	"github.com/luck/go-luck/internal/debug"
	"github.com/luck/go-luck/les"
	"github.com/luck/go-luck/node"
	"github.com/luck/go-luck/p2p"
	"github.com/luck/go-luck/p2p/nat"
	"github.com/luck/go-luck/params"
	whisper "github.com/luck/go-luck/whisper/whisperv6"
)

// NodeConfig represents the collection of configuration values to fine tune the Gfort
// node embedded into a mobile process. The available values are a subset of the
// entire API provided by go-luck to reduce the maintenance surface and dev
// complexity.
type NodeConfig struct {
	// Bootstrap nodes used to establish connectivity with the rest of the network.
	BootstrapNodes *Enodes

	// MaxPeers is the maximum number of peers that can be connected. If this is
	// set to zero, then only the configured static and trusted peers can connect.
	MaxPeers int

	// LuckEnabled specifies whforter the node should run the Luck protocol.
	LuckEnabled bool

	// LuckNetworkID is the network identifier used by the Luck protocol to
	// decide if remote peers should be accepted or not.
	LuckNetworkID int64 // uint64 in truth, but Java can't handle that...

	// LuckGenesis is the genesis JSON to use to seed the blockchain with. An
	// empty genesis state is equivalent to using the mainnet's state.
	LuckGenesis string

	// LuckDatabaseCache is the system memory in MB to allocate for database caching.
	// A minimum of 16MB is always reserved.
	LuckDatabaseCache int

	// LuckNetStats is a netstats connection string to use to report various
	// chain, transaction and node stats to a monitoring server.
	//
	// It has the form "nodename:secret@host:port"
	LuckNetStats string

	// WhisperEnabled specifies whforter the node should run the Whisper protocol.
	WhisperEnabled bool

	// Listening address of pprof server.
	PprofAddress string
}

// defaultNodeConfig contains the default node configuration values to use if all
// or some fields are missing from the user's specified list.
var defaultNodeConfig = &NodeConfig{
	BootstrapNodes:        FoundationBootnodes(),
	MaxPeers:              25,
	LuckEnabled:       true,
	LuckNetworkID:     1,
	LuckDatabaseCache: 16,
}

// NewNodeConfig creates a new node option set, initialized to the default values.
func NewNodeConfig() *NodeConfig {
	config := *defaultNodeConfig
	return &config
}

// Node represents a Gfort Luck node instance.
type Node struct {
	node *node.Node
}

// NewNode creates and configures a new Gfort node.
func NewNode(datadir string, config *NodeConfig) (stack *Node, _ error) {
	// If no or partial configurations were specified, use defaults
	if config == nil {
		config = NewNodeConfig()
	}
	if config.MaxPeers == 0 {
		config.MaxPeers = defaultNodeConfig.MaxPeers
	}
	if config.BootstrapNodes == nil || config.BootstrapNodes.Size() == 0 {
		config.BootstrapNodes = defaultNodeConfig.BootstrapNodes
	}

	if config.PprofAddress != "" {
		debug.StartPProf(config.PprofAddress)
	}

	// Create the empty networking stack
	nodeConf := &node.Config{
		Name:        clientIdentifier,
		Version:     params.VersionWithMeta,
		DataDir:     datadir,
		KeyStoreDir: filepath.Join(datadir, "keystore"), // Mobile should never use internal keystores!
		P2P: p2p.Config{
			NoDiscovery:      true,
			DiscoveryV5:      true,
			BootstrapNodesV5: config.BootstrapNodes.nodes,
			ListenAddr:       ":0",
			NAT:              nat.Any(),
			MaxPeers:         config.MaxPeers,
		},
	}

	rawStack, err := node.New(nodeConf)
	if err != nil {
		return nil, err
	}

	debug.Memsize.Add("node", rawStack)

	var genesis *core.Genesis
	if config.LuckGenesis != "" {
		// Parse the user supplied genesis spec if not mainnet
		genesis = new(core.Genesis)
		if err := json.Unmarshal([]byte(config.LuckGenesis), genesis); err != nil {
			return nil, fmt.Errorf("invalid genesis spec: %v", err)
		}
		// If we have the Ropsten testnet, hard code the chain configs too
		if config.LuckGenesis == RopstenGenesis() {
			genesis.Config = params.RopstenChainConfig
			if config.LuckNetworkID == 1 {
				config.LuckNetworkID = 3
			}
		}
		// If we have the Testnet testnet, hard code the chain configs too
		if config.LuckGenesis == TestnetGenesis() {
			genesis.Config = params.TestnetChainConfig
			if config.LuckNetworkID == 1 {
				config.LuckNetworkID = 4
			}
		}
		// If we have the Goerli testnet, hard code the chain configs too
		if config.LuckGenesis == GoerliGenesis() {
			genesis.Config = params.GoerliChainConfig
			if config.LuckNetworkID == 1 {
				config.LuckNetworkID = 5
			}
		}
	}
	// Register the Luck protocol if requested
	if config.LuckEnabled {
		fortConf := fort.DefaultConfig
		fortConf.Genesis = genesis
		fortConf.SyncMode = downloader.LightSync
		fortConf.NetworkId = uint64(config.LuckNetworkID)
		fortConf.DatabaseCache = config.LuckDatabaseCache
		if err := rawStack.Register(func(ctx *node.ServiceContext) (node.Service, error) {
			return les.New(ctx, &fortConf)
		}); err != nil {
			return nil, fmt.Errorf("luck init: %v", err)
		}
		// If netstats reporting is requested, do it
		if config.LuckNetStats != "" {
			if err := rawStack.Register(func(ctx *node.ServiceContext) (node.Service, error) {
				var lesServ *les.LightLuck
				ctx.Service(&lesServ)

				return fortstats.New(config.LuckNetStats, nil, lesServ)
			}); err != nil {
				return nil, fmt.Errorf("netstats init: %v", err)
			}
		}
	}
	// Register the Whisper protocol if requested
	if config.WhisperEnabled {
		if err := rawStack.Register(func(*node.ServiceContext) (node.Service, error) {
			return whisper.New(&whisper.DefaultConfig), nil
		}); err != nil {
			return nil, fmt.Errorf("whisper init: %v", err)
		}
	}
	return &Node{rawStack}, nil
}

// Close terminates a running node along with all it's services, tearing internal
// state doen too. It's not possible to restart a closed node.
func (n *Node) Close() error {
	return n.node.Close()
}

// Start creates a live P2P node and starts running it.
func (n *Node) Start() error {
	return n.node.Start()
}

// Stop terminates a running node along with all it's services. If the node was
// not started, an error is returned.
func (n *Node) Stop() error {
	return n.node.Stop()
}

// GetLuckClient retrieves a client to access the Luck subsystem.
func (n *Node) GetLuckClient() (client *LuckClient, _ error) {
	rpc, err := n.node.Attach()
	if err != nil {
		return nil, err
	}
	return &LuckClient{fortclient.NewClient(rpc)}, nil
}

// GetNodeInfo gathers and returns a collection of metadata known about the host.
func (n *Node) GetNodeInfo() *NodeInfo {
	return &NodeInfo{n.node.Server().NodeInfo()}
}

// GetPeersInfo returns an array of metadata objects describing connected peers.
func (n *Node) GetPeersInfo() *PeerInfos {
	return &PeerInfos{n.node.Server().PeersInfo()}
}

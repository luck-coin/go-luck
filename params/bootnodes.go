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

package params

import "github.com/luck/go-luck/common"

// MainnetBootnodes are the lnode URLs of the P2P bootstrap nodes running on
// the main Luck network.
var MainnetBootnodes = []string{
	// Luck Foundation Go Bootnodes
	"lnode://f4cce3c61d0eca3e9761d8031abf92e9e1c38f1ce82399af26f6ba8941b4232843e38b8833f499133fed0e9c15a83c0e406be309d0aa732040845e00f6d6ce81@161.97.100.255:30009",
	"lnode://2a89997079cb7f6d1922c70ca011e0b81554908afe3787645e26a9968d384cfaecc5631dfe73193258a3ac2124d42eaef8b4f56627aa29d38013f1b3930a9192@34.72.40.50:30009",
	"lnode://3b6aebc8fde13f76549ccfbaed12154b8dbe558225415fd0cdbac1feeb823686278587a793185f127214b55ee68ab51524488d345428caed084d3ae58d0b7cb7@34.121.75.40:30009",
	"lnode://eade275115764d4565610e7ca68309af2688ad85964ca3db315e895841f331056e37cafd2a1ab7f374e8caba781e021ee13bb3c68d69394b79eeeb4d1eaa2619@34.69.167.97:30009",
	"lnode://6bd274130eb167fc437f34ff718fec5717a9d6d2cd744846405a7b6d0e36839136ab1e1637e14de00c579fd2721c1972d175756ed5404f3f2dab21982a290eb4@34.121.90.94:30009",
	"lnode://f3c4856a973ae1a52285535b17e188fb60c10093607abb858d4c916189319b6846f6b49d3ec9375832b32d853f6719c96b72c09d20e261e004de217acac9a1a7@34.97.236.146:30009",
	"lnode://074900a8a9e49fc93b9203722bb5764cf2d8cfa84bdabec5b764410e9622d8b91ca51bae49af57961230d70302ce81bfca4f6a98f385fb7e350961a661bc9b5b@35.228.145.114:30009",
	"lnode://0df47b06dd50f3b25eddd30567b136edc28ed5b02313ed562607649634cc3b24ba9e4a846d215706692a6cb5c4e91f4e9e444d1b9cb4a08548e6b0fad9199462@46.243.187.56:30009",
	"lnode://a3c32e08ec6eb7ed1747741406346624c515dcfafa92b8c3a853b89b3911077229b8f8606c6ceef9a30359c945eb7d95da71e41d6d1d3361bbfc4e9e5954dc4e@188.227.57.147:30009",
	"lnode://8907c552f673a0566e0c4d85e8bebc54319551d7d35ea9cf3c06f1c453a979a7d991ecd440e46b828ead86ed924cd7c8cb470340ac3e9aa7e2da950c13772743@8.208.8.145:30009",
	"lnode://2b6c9515602b6bbd53cf0ad82c969a38b65c3e4399a4c8264a1e009c7a1a573e76710c85c384a9736c169166c1a98504aca653a46bf2ad9fcfdd9b6f25d58aa8@8.211.54.254:30009",
	//"lnode://1f7702c8419c35512d937b88036cd1b8a45f4e6650805eea58fe5ec55c992075a1a28c336e4582f3ad5378afe85417987b77fe22620607e98d999e9c8bcf6438@47.74.55.77:30009",
}

// RopstenBootnodes are the lnode URLs of the P2P bootstrap nodes running on the
// Ropsten test network.
var RopstenBootnodes = []string{
}

// TestnetBootnodes are the lnode URLs of the P2P bootstrap nodes running on the
// Testnet test network.
var TestnetBootnodes = []string{
	
	//"lnode://10d0d42dd67b938a857a15bb41aa5c4ae0d074524af06257f510922a73a7cf4dca8420b7ed1445cacc9eb81cd9273897417ee64928d392e0f9caa66886b22b7b@47.74.55.77:30009",
}

// GoerliBootnodes are the lnode URLs of the P2P bootstrap nodes running on the
// Görli test network.
var GoerliBootnodes = []string{
}

// DiscoveryV5Bootnodes are the lnode URLs of the P2P bootstrap nodes for the
// experimental RLPx v5 topic-discovery network.
var DiscoveryV5Bootnodes = []string{
}

const dnsPrefix = "enrtree://AKA3AM6LPBYEUDMVNU3BSVQJ5AD45Y7YPOHJLEF6W26QOE4VTUDPE@"

// These DNS names provide bootstrap connectivity for public testnets and the mainnet.
// See https://github.com/luck/discv4-dns-lists for more information.
var KnownDNSNetworks = map[common.Hash]string{
	MainnetGenesisHash: dnsPrefix + "all.mainnet.fortdisco.net",
	RopstenGenesisHash: dnsPrefix + "all.ropsten.fortdisco.net",
	TestnetGenesisHash: dnsPrefix + "all.lucktest.fortdisco.net",
	GoerliGenesisHash:  dnsPrefix + "all.goerli.fortdisco.net",
}

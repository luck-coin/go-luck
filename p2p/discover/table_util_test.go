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

package discover

import (
	"bytes"
	"crypto/ecdsa"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"sort"
	"sync"

	"github.com/luck/go-luck/crypto"
	"github.com/luck/go-luck/log"
	"github.com/luck/go-luck/p2p/lnode"
	"github.com/luck/go-luck/p2p/enr"
)

var nullNode *lnode.Node

func init() {
	var r enr.Record
	r.Set(enr.IP{0, 0, 0, 0})
	nullNode = lnode.SignNull(&r, lnode.ID{})
}

func newTestTable(t transport) (*Table, *lnode.DB) {
	db, _ := lnode.OpenDB("")
	tab, _ := newTable(t, db, nil, log.Root())
	go tab.loop()
	return tab, db
}

// nodeAtDistance creates a node for which lnode.LogDist(base, n.id) == ld.
func nodeAtDistance(base lnode.ID, ld int, ip net.IP) *node {
	var r enr.Record
	r.Set(enr.IP(ip))
	return wrapNode(lnode.SignNull(&r, idAtDistance(base, ld)))
}

// nodesAtDistance creates n nodes for which lnode.LogDist(base, node.ID()) == ld.
func nodesAtDistance(base lnode.ID, ld int, n int) []*lnode.Node {
	results := make([]*lnode.Node, n)
	for i := range results {
		results[i] = unwrapNode(nodeAtDistance(base, ld, intIP(i)))
	}
	return results
}

func nodesToRecords(nodes []*lnode.Node) []*enr.Record {
	records := make([]*enr.Record, len(nodes))
	for i := range nodes {
		records[i] = nodes[i].Record()
	}
	return records
}

// idAtDistance returns a random hash such that lnode.LogDist(a, b) == n
func idAtDistance(a lnode.ID, n int) (b lnode.ID) {
	if n == 0 {
		return a
	}
	// flip bit at position n, fill the rest with random bits
	b = a
	pos := len(a) - n/8 - 1
	bit := byte(0x01) << (byte(n%8) - 1)
	if bit == 0 {
		pos++
		bit = 0x80
	}
	b[pos] = a[pos]&^bit | ^a[pos]&bit // TODO: randomize end bits
	for i := pos + 1; i < len(a); i++ {
		b[i] = byte(rand.Intn(255))
	}
	return b
}

func intIP(i int) net.IP {
	return net.IP{byte(i), 0, 2, byte(i)}
}

// fillBucket inserts nodes into the given bucket until it is full.
func fillBucket(tab *Table, n *node) (last *node) {
	ld := lnode.LogDist(tab.self().ID(), n.ID())
	b := tab.bucket(n.ID())
	for len(b.entries) < bucketSize {
		b.entries = append(b.entries, nodeAtDistance(tab.self().ID(), ld, intIP(ld)))
	}
	return b.entries[bucketSize-1]
}

// fillTable adds nodes the table to the end of their corresponding bucket
// if the bucket is not full. The caller must not hold tab.mutex.
func fillTable(tab *Table, nodes []*node) {
	for _, n := range nodes {
		tab.addSeenNode(n)
	}
}

type pingRecorder struct {
	mu           sync.Mutex
	dead, pinged map[lnode.ID]bool
	records      map[lnode.ID]*lnode.Node
	n            *lnode.Node
}

func newPingRecorder() *pingRecorder {
	var r enr.Record
	r.Set(enr.IP{0, 0, 0, 0})
	n := lnode.SignNull(&r, lnode.ID{})

	return &pingRecorder{
		dead:    make(map[lnode.ID]bool),
		pinged:  make(map[lnode.ID]bool),
		records: make(map[lnode.ID]*lnode.Node),
		n:       n,
	}
}

// setRecord updates a node record. Future calls to ping and
// requestENR will return this record.
func (t *pingRecorder) updateRecord(n *lnode.Node) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.records[n.ID()] = n
}

// Stubs to satisfy the transport interface.
func (t *pingRecorder) Self() *lnode.Node           { return nullNode }
func (t *pingRecorder) lookupSelf() []*lnode.Node   { return nil }
func (t *pingRecorder) lookupRandom() []*lnode.Node { return nil }
func (t *pingRecorder) close()                      {}

// ping simulates a ping request.
func (t *pingRecorder) ping(n *lnode.Node) (seq uint64, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.pinged[n.ID()] = true
	if t.dead[n.ID()] {
		return 0, errTimeout
	}
	if t.records[n.ID()] != nil {
		seq = t.records[n.ID()].Seq()
	}
	return seq, nil
}

// requestENR simulates an ENR request.
func (t *pingRecorder) RequestENR(n *lnode.Node) (*lnode.Node, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.dead[n.ID()] || t.records[n.ID()] == nil {
		return nil, errTimeout
	}
	return t.records[n.ID()], nil
}

func hasDuplicates(slice []*node) bool {
	seen := make(map[lnode.ID]bool)
	for i, e := range slice {
		if e == nil {
			panic(fmt.Sprintf("nil *Node at %d", i))
		}
		if seen[e.ID()] {
			return true
		}
		seen[e.ID()] = true
	}
	return false
}

func checkNodesEqual(got, want []*lnode.Node) error {
	if len(got) == len(want) {
		for i := range got {
			if !nodeEqual(got[i], want[i]) {
				goto NotEqual
			}
			return nil
		}
	}

NotEqual:
	output := new(bytes.Buffer)
	fmt.Fprintf(output, "got %d nodes:\n", len(got))
	for _, n := range got {
		fmt.Fprintf(output, "  %v %v\n", n.ID(), n)
	}
	fmt.Fprintf(output, "want %d:\n", len(want))
	for _, n := range want {
		fmt.Fprintf(output, "  %v %v\n", n.ID(), n)
	}
	return errors.New(output.String())
}

func nodeEqual(n1 *lnode.Node, n2 *lnode.Node) bool {
	return n1.ID() == n2.ID() && n1.IP().Equal(n2.IP())
}

func sortByID(nodes []*lnode.Node) {
	sort.Slice(nodes, func(i, j int) bool {
		return string(nodes[i].ID().Bytes()) < string(nodes[j].ID().Bytes())
	})
}

func sortedByDistanceTo(distbase lnode.ID, slice []*node) bool {
	return sort.SliceIsSorted(slice, func(i, j int) bool {
		return lnode.DistCmp(distbase, slice[i].ID(), slice[j].ID()) < 0
	})
}

func hexEncPrivkey(h string) *ecdsa.PrivateKey {
	b, err := hex.DecodeString(h)
	if err != nil {
		panic(err)
	}
	key, err := crypto.ToECDSA(b)
	if err != nil {
		panic(err)
	}
	return key
}

func hexEncPubkey(h string) (ret encPubkey) {
	b, err := hex.DecodeString(h)
	if err != nil {
		panic(err)
	}
	if len(b) != len(ret) {
		panic("invalid length")
	}
	copy(ret[:], b)
	return ret
}

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

package les

import (
	"errors"

	"github.com/luck/go-luck/log"
	"github.com/luck/go-luck/p2p/lnode"
)

type ulc struct {
	keys     map[string]bool
	fraction int
}

// newULC creates and returns an ultra light client instance.
func newULC(servers []string, fraction int) (*ulc, error) {
	keys := make(map[string]bool)
	for _, id := range servers {
		node, err := lnode.Parse(lnode.ValidSchemes, id)
		if err != nil {
			log.Warn("Failed to parse trusted server", "id", id, "err", err)
			continue
		}
		keys[node.ID().String()] = true
	}
	if len(keys) == 0 {
		return nil, errors.New("no trusted servers")
	}
	return &ulc{
		keys:     keys,
		fraction: fraction,
	}, nil
}

// trusted return an indicator that whforter the specified peer is trusted.
func (u *ulc) trusted(p lnode.ID) bool {
	return u.keys[p.String()]
}

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

// Contains the metrics collected by the downloader.

package downloader

import (
	"github.com/luck/go-luck/metrics"
)

var (
	headerInMeter      = metrics.NewRegisteredMeter("fort/downloader/headers/in", nil)
	headerReqTimer     = metrics.NewRegisteredTimer("fort/downloader/headers/req", nil)
	headerDropMeter    = metrics.NewRegisteredMeter("fort/downloader/headers/drop", nil)
	headerTimeoutMeter = metrics.NewRegisteredMeter("fort/downloader/headers/timeout", nil)

	bodyInMeter      = metrics.NewRegisteredMeter("fort/downloader/bodies/in", nil)
	bodyReqTimer     = metrics.NewRegisteredTimer("fort/downloader/bodies/req", nil)
	bodyDropMeter    = metrics.NewRegisteredMeter("fort/downloader/bodies/drop", nil)
	bodyTimeoutMeter = metrics.NewRegisteredMeter("fort/downloader/bodies/timeout", nil)

	receiptInMeter      = metrics.NewRegisteredMeter("fort/downloader/receipts/in", nil)
	receiptReqTimer     = metrics.NewRegisteredTimer("fort/downloader/receipts/req", nil)
	receiptDropMeter    = metrics.NewRegisteredMeter("fort/downloader/receipts/drop", nil)
	receiptTimeoutMeter = metrics.NewRegisteredMeter("fort/downloader/receipts/timeout", nil)

	stateInMeter   = metrics.NewRegisteredMeter("fort/downloader/states/in", nil)
	stateDropMeter = metrics.NewRegisteredMeter("fort/downloader/states/drop", nil)
)

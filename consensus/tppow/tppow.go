package tppow

import (
	"fmt"
	"errors"
	"github.com/luck/go-luck/common"
	//"github.com/luck/go-luck/crypto"
	"github.com/luck/go-luck/core/types"
	"github.com/luck/go-luck/consensus"
	"github.com/luck/go-luck/core/state"
	"github.com/luck/go-luck/rpc"
	//lru "github.com/hashicorp/golang-lru"
	"github.com/luck/go-luck/params"
	"github.com/luck/go-luck/crypto"
	"time"
	//"io/ioutil"
	//"strings"
	//"strconv"
	"math/big"
	"math/rand"
	"math"
	"sync"
	//"os"
	"runtime"
	//"github.com/luck/go-luck/common/math"
	//"github.com/luck/go-luck/node"
	//set "gopkg.in/fatih/set.v0"
	mapset "github.com/deckarep/golang-set"
	"github.com/luck/go-luck/rlp"
	"golang.org/x/crypto/sha3"
	crand "crypto/rand"
)	

var (

	blockReward  *big.Int = big.NewInt(1e+18)

	maxLuck *big.Int = big.NewInt(2e+8)

	HashScale *big.Int = big.NewInt(4e+18)

	initBasis *big.Int = new(big.Int).Sub(new(big.Int).Lsh(common.Big1, 186), common.Big1)
	initDifficultyAlpha *big.Int = new(big.Int).Sub(new(big.Int).Lsh(common.Big1, 190), common.Big1)
	max256 *big.Int = new(big.Int).Sub(new(big.Int).Lsh(common.Big1, 256), common.Big1)

	difficultyAjustBlock uint64 = uint64(39200) 

	maxUncles                     = 2 // Maximum number of uncles allowed in a single block

	allowedFutureBlockTime    = 15 * time.Second  // Max time from current time allowed for blocks, before they're considered future blocks

	// errLargeBlockTime    = errors.New("timestamp too big")
	// errZeroBlockTime     = errors.New("timestamp equals parent's")
	errOlderBlockTime    = errors.New("timestamp older than parent")
	errTooManyUncles     = errors.New("too many uncles")
	errDuplicateUncle    = errors.New("duplicate uncle")
	errUncleIsAncestor   = errors.New("uncle is ancestor")
	errDanglingUncle     = errors.New("uncle's parent is not ancestor")

	errInconsistence     = errors.New("param inconsistence")
	errComputeLucky      = errors.New("compute the luck")
	errUnknownBlock      = errors.New("mined block unknown")
)

type Tppow struct {
	rand     *rand.Rand    // Properly seeded random source for nonces
	lock      sync.Mutex // Ensures thread safety for the in-memory caches and mining fields
}

func New() *Tppow {
	return &Tppow{}
}

func (d *Tppow) Author(header *types.Header) (common.Address, error) {
	return header.Coinbase, nil
}

func (d *Tppow) CalcDifficulty(chain consensus.ChainReader, time uint64, parent *types.Header) *big.Int {
	// snap, err := c.snapshot(chain, parent.Number.Uint64(), parent.Hash(), nil)
	// if err != nil {
	// 	return nil
	// }
	// return CalcDifficulty(snap, c.signer)
	return big.NewInt(1)
}

func (d *Tppow) VerifyHeader(chain consensus.ChainReader, header *types.Header, seal bool) error {
	number := header.Number.Uint64()
	if chain.GetHeader(header.Hash(), number) != nil {
		return nil
	}
	parent := chain.GetHeader(header.ParentHash, number-1)
	if parent == nil {
		return consensus.ErrUnknownAncestor
	}
	return d.verifyHeader(chain, header, parent, false, seal)
}

func (d *Tppow) VerifyHeaders(chain consensus.ChainReader, headers []*types.Header, seals []bool) (chan<- struct{}, <-chan error) {
	if len(headers) == 0 {
		abort, results := make(chan struct{}), make(chan error, len(headers))
		for i := 0; i < len(headers); i++ {
			results <- nil
		}
		return abort, results
	}

	// Spawn as many workers as allowed threads
	workers := runtime.GOMAXPROCS(0)
	if len(headers) < workers {
		workers = len(headers)
	}

	// Create a task channel and spawn the verifiers
	var (
		inputs = make(chan int)
		done   = make(chan int, workers)
		errors = make([]error, len(headers))
		abort  = make(chan struct{})
	)
	for i := 0; i < workers; i++ {
		go func() {
			for index := range inputs {
				errors[index] = d.verifyHeaderWorker(chain, headers, seals, index)
				done <- index
			}
		}()
	}

	errorsOut := make(chan error, len(headers))
	go func() {
		defer close(inputs)
		var (
			in, out = 0, 0
			checked = make([]bool, len(headers))
			inputs  = inputs
		)
		for {
			select {
			case inputs <- in:
				if in++; in == len(headers) {
					// Reached end of headers. Stop sending to workers.
					inputs = nil
				}
			case index := <-done:
				for checked[index] = true; checked[out]; out++ {
					errorsOut <- errors[out]
					if out == len(headers)-1 {
						return
					}
				}
			case <-abort:
				return
			}
		}
	}()
	return abort, errorsOut
}

func (d *Tppow) verifyHeaderWorker(chain consensus.ChainReader, headers []*types.Header, seals []bool, index int) error {
	var parent *types.Header
	if index == 0 {
		parent = chain.GetHeader(headers[0].ParentHash, headers[0].Number.Uint64()-1)
	} else if headers[index-1].Hash() == headers[index].ParentHash {
		parent = headers[index-1]
	}
	if parent == nil {
		return consensus.ErrUnknownAncestor
	}
	if chain.GetHeader(headers[index].Hash(), headers[index].Number.Uint64()) != nil {
		return nil // known block
	}
	return d.verifyHeader(chain, headers[index], parent, false, seals[index])
}

func (d *Tppow) verifyHeader(chain consensus.ChainReader, header *types.Header, parent *types.Header, uncle bool, seal bool) error {
	// Ensure that the header's extra-data section is of a reasonable size
	if uint64(len(header.Extra)) > params.MaximumExtraDataSize {
		return fmt.Errorf("extra-data too long: %d > %d", len(header.Extra), params.MaximumExtraDataSize)
	}
	// Verify the header's timestamp
	if !uncle {
		if header.Time > uint64(time.Now().Add(allowedFutureBlockTime).Unix()) {
			return consensus.ErrFutureBlock
		}
	}
	if header.Time <= parent.Time {
		return errOlderBlockTime
	}
	// Verify the block's difficulty based on its timestamp and parent's difficulty
	// expected := ethash.CalcDifficulty(chain, header.Time, parent)

	// if expected.Cmp(header.Difficulty) != 0 {
	// 	return fmt.Errorf("invalid difficulty: have %v, want %v", header.Difficulty, expected)
	// }
	// Verify that the gas limit is <= 2^63-1
	cap := uint64(0x7fffffffffffffff)
	if header.GasLimit > cap {
		return fmt.Errorf("invalid gasLimit: have %v, max %v", header.GasLimit, cap)
	}
	// Verify that the gasUsed is <= gasLimit
	if header.GasUsed > header.GasLimit {
		return fmt.Errorf("invalid gasUsed: have %d, gasLimit %d", header.GasUsed, header.GasLimit)
	}

	// Verify that the gas limit remains within allowed bounds
	diff := int64(parent.GasLimit) - int64(header.GasLimit)
	if diff < 0 {
		diff *= -1
	}
	limit := parent.GasLimit / params.GasLimitBoundDivisor

	if uint64(diff) >= limit || header.GasLimit < params.MinGasLimit {
		return fmt.Errorf("invalid gas limit: have %d, want %d += %d", header.GasLimit, parent.GasLimit, limit)
	}
	// Verify that the block number is parent's +1
	if diff := new(big.Int).Sub(header.Number, parent.Number); diff.Cmp(big.NewInt(1)) != 0 {
		return consensus.ErrInvalidNumber
	}

	number := header.Number
	basis, alpha := d.calcParam(chain, number.Uint64(), header.Time, parent)

	if basis.Cmp(header.Basis) != 0 {
		return errInconsistence
	}

	if alpha.Cmp(header.DifficultyAlpha) != 0 {
		return errInconsistence
	}

	// Verify the engine specific seal securing the block
	if seal {
		if err := d.VerifySeal(chain, header); err != nil {
			return err
		}
	}
	return nil
}

func (d *Tppow) verifyCascadingFields(chain consensus.ChainReader, header *types.Header, parents []*types.Header) error {
	return nil
}

func (d *Tppow) VerifyUncles(chain consensus.ChainReader, block *types.Block) error {
	// Verify that there are at most 2 uncles included in this block
	if len(block.Uncles()) > maxUncles {
		return errTooManyUncles
	}
	// Gather the set of past uncles and ancestors
	uncles, ancestors := mapset.NewSet(), make(map[common.Hash]*types.Header)

	number, parent := block.NumberU64()-1, block.ParentHash()
	for i := 0; i < 7; i++ {
		ancestor := chain.GetBlock(parent, number)
		if ancestor == nil {
			break
		}
		ancestors[ancestor.Hash()] = ancestor.Header()
		for _, uncle := range ancestor.Uncles() {
			uncles.Add(uncle.Hash())
		}
		parent, number = ancestor.ParentHash(), number-1
	}
	ancestors[block.Hash()] = block.Header()
	uncles.Add(block.Hash())

	// Verify each of the uncles that it's recent, but not an ancestor
	for _, uncle := range block.Uncles() {
		// Make sure every uncle is rewarded only once
		hash := uncle.Hash()
		if uncles.Contains(hash) {
			return errDuplicateUncle
		}
		uncles.Add(hash)

		// Make sure the uncle has a valid ancestry
		if ancestors[hash] != nil {
			return errUncleIsAncestor
		}
		if ancestors[uncle.ParentHash] == nil || uncle.ParentHash == block.ParentHash() {
			return errDanglingUncle
		}
		if err := d.verifyHeader(chain, uncle, ancestors[uncle.ParentHash], true, true); err != nil {
			return err
		}
	}
	return nil
}

func (d *Tppow) VerifySeal(chain consensus.ChainReader, header *types.Header) error {
	aHash := d.SealLuck(header, header.FirstNonce.Uint64())
	if aHash.Cmp(header.DifficultyAlpha) >= 0{
		return errUnknownBlock
	}

	beta := d.calcBeta(header.Lucky, header.Basis)


	if beta.Cmp(header.DifficultyBeta) != 0 {
		return errInconsistence
	}

	sl := d.calcLuck(header, header.FirstNonce.Uint64())

	if sl.Cmp(header.Lucky) != 0 {
		return errComputeLucky
	}

	b := d.SealBlock(header, header.SecondNonce.Uint64())
	if b.Cmp(header.DifficultyBeta) >= 0 {
		return errUnknownBlock
	}

	diff := d.calcDifficulty(header)
	if diff.Cmp(header.Difficulty) != 0 {
		return errUnknownBlock
	}

	return nil
}

func (d *Tppow) Prepare(chain consensus.ChainReader, header *types.Header) error {
	parent := chain.GetHeader(header.ParentHash, header.Number.Uint64()-1)
	if parent == nil {
		return consensus.ErrUnknownAncestor
	}
	header.Basis, header.DifficultyAlpha = d.calcParam(chain, header.Number.Uint64(), header.Time, parent)
		
	//fmt.Printf("parent.Time=%v, parent.DifficultyAlpha=%v, parent.DifficultyBeta=%v, parent.Basis=%v\r\n ",
	//	parent.Time, parent.DifficultyAlpha, parent.DifficultyBeta, parent.Basis, )

	return nil
}

func (d *Tppow) Finalize(chain consensus.ChainReader, header *types.Header, state *state.StateDB, txs []*types.Transaction, uncles []*types.Header) {
	mineRewards(chain.Config(), state, header, uncles)
	header.Root = state.IntermediateRoot(chain.Config().IsEIP158(header.Number))
	// Header seems complete, assemble into a block and return
	//return types.NewBlock(header, txs, uncles, receipts), nil
}

func (d *Tppow) FinalizeAndAssemble(chain consensus.ChainReader, header *types.Header, state *state.StateDB, txs []*types.Transaction, uncles []*types.Header, receipts []*types.Receipt) (*types.Block, error) {
	mineRewards(chain.Config(), state, header, uncles)
	header.Root = state.IntermediateRoot(chain.Config().IsEIP158(header.Number))
	header.UncleHash = types.CalcUncleHash(nil)

	// Assemble and return the final block for sealing
	return types.NewBlock(header, txs, nil, receipts), nil
}

func (d *Tppow) Seal(chain consensus.ChainReader, block *types.Block, results chan<- *types.Block, stop <-chan struct{}) (error) {
	//number := block.Number()
	abort := make(chan struct{})
	found := make(chan *types.Block)

	d.lock.Lock()
	if d.rand == nil {
		seed, err := crand.Int(crand.Reader, big.NewInt(math.MaxInt64))
		if err != nil {
			d.lock.Unlock()
			return err
		}
		d.rand = rand.New(rand.NewSource(seed.Int64()))
	}
	d.lock.Unlock()


	go func(fn uint64, sn uint64) {
		d.mine(block, fn, sn, abort, found)
	}(uint64(d.rand.Int63()), uint64(d.rand.Int63()))

	var result *types.Block
	select {
	case <-stop:
		// Outside abort, stop all miner threads
		close(abort)
	case result = <-found:
		select {
		case results <- result:
		default:
			fmt.Printf("not ready\r\n")
		}
		// One of the threads found a block, abort all others
		close(abort)
	}
	// Wait for all miners to terminate and return the block
	return nil
}

func (d *Tppow) mine(block *types.Block, fn uint64, sn uint64, abort chan struct{}, found chan *types.Block) {
	var (
		header = block.Header()
		firstNonce = fn
		secondNonce = sn
		lucky = big.NewInt(0)

		currFirstNonce = fn
	)
	//header.DifficultyBeta = new(big.Int).Set(beta)

search_luck:
	for {
		select {
		case <-abort:
			// Mining terminated, update stats and abort
			fmt.Printf("First nonce search aborted, firstNonce=%v\r\n", currFirstNonce)
			//ethash.hashrate.Mark(attempts)
			goto search_end
			//break search
		default:
			currFirstNonce++


			aHash := d.SealLuck(header, currFirstNonce)


			if aHash.Cmp(header.DifficultyAlpha) < 0 {
				firstNonce = currFirstNonce
				lucky = d.calcLuck(header, firstNonce)
				break search_luck
			}
		}
	}
	header.Lucky = new(big.Int).Set(lucky)
	header.FirstNonce = types.EncodeNonce(firstNonce)
	header.DifficultyBeta = d.calcBeta(lucky, header.Basis)
	//header.Difficulty = d.calcDifficulty(lucky)
	header.Difficulty = d.calcDifficulty(header)

	//fmt.Printf("header.difficultybeta=%v, lucky=%v\r\n", header.DifficultyBeta, lucky)

	//fmt.Printf("header.Time=%v, header.DifficultyAlpha=%v, header.Lucky=%v, header.DifficultyBeta=%v, header.Basis=%v\r\n",
	//	header.Time, header.DifficultyAlpha, header.Lucky, header.DifficultyBeta, header.Basis)

search_block:	
	for {
		select {
		case <-abort:
			// Mining terminated, update stats and abort
			fmt.Printf("Second nonce search aborted, nonce=%v\r\n", secondNonce)
			break search_block

		default:
			// We don't have to update hash rate on every nonce, so update after after 2^X nonces
			secondNonce++

			b := d.SealBlock(header, secondNonce)
			if b.Cmp(header.DifficultyBeta) < 0 {
				header.SecondNonce = types.EncodeNonce(secondNonce)
				header = types.CopyHeader(header)
				select {
				case found <- block.WithSeal(header):
					fmt.Printf("Luck nonce found and reported, lucky=%v, firstNonce=%v, secondNonce=%v\r\n", lucky, firstNonce, secondNonce)
				case <-abort:
					fmt.Printf("Luck nonce found but discarded lucky=%v, firstNonce=%v, secondNonce=%v\r\n", lucky, firstNonce, secondNonce)
				}
				break search_block
			}
		}
	}

search_end:
	//fmt.Printf("exit seal\r\n")
}

func (d *Tppow) SealLuck(header *types.Header, nonce uint64) (*big.Int) {
	bs, _ := rlp.EncodeToBytes([]interface{}{
		header.ParentHash,
		header.Coinbase,
		header.Time,
		nonce,
	})


	hash := crypto.Argon2Hash(bs, header.ParentHash.Bytes()[22:])
	res := new(big.Int).SetBytes(hash)
	res = res.Div(res, HashScale)
	//res = res.Div(res, header.DifficultyAlpha)
	return res
}

func (d *Tppow) calcLuck(header *types.Header, nonce uint64) (*big.Int) {
	//return big.NewInt(1000000)
	bs, _ := rlp.EncodeToBytes([]interface{}{
		header.ParentHash,
		header.Coinbase,
		header.Time,
		header.Number,
		nonce,
	})

	//fmt.Printf("222222 bytes=%v\r\n", header.ParentHash.Bytes()[20:])

	hash := crypto.Argon2Hash(bs, header.ParentHash.Bytes()[20:])
	res := new(big.Int).SetBytes(hash)
	res = res.Mod(res, maxLuck)
	return res
}

func (d *Tppow) SealBlock(header *types.Header, nonce uint64) (*big.Int) {
	bs, _ := rlp.EncodeToBytes([]interface{}{
		header.ParentHash,
		header.UncleHash,
		header.Coinbase,
		header.Root,
		header.TxHash,
		header.ReceiptHash,
		header.Bloom,
		//header.Difficulty,
		header.Number,
		header.GasLimit,
		header.GasUsed,
		header.Time,
		header.Extra,
		//header.Range,
		header.Basis,
		header.Lucky,
		header.DifficultyAlpha,
		header.DifficultyBeta,
		//header.Difficulty,
		nonce,
	})

	//fmt.Printf("333333 bytes=%v\r\n", header.ParentHash.Bytes()[22:])

	hash := crypto.Argon2Hash(bs, header.ParentHash.Bytes()[22:])
	res := new(big.Int).SetBytes(hash)
	res = res.Div(res, HashScale)
	//res = res.Div(res, header.DifficultyAlpha)
	return res
}
 
func (d *Tppow) SealHash(header *types.Header) (hash common.Hash) {
	hasher := sha3.NewLegacyKeccak256()

	rlp.Encode(hasher, []interface{}{
		header.ParentHash,
		header.UncleHash,
		header.Coinbase,
		header.Root,
		header.TxHash,
		header.ReceiptHash,
		header.Bloom,
		//header.Difficulty,
		header.Number,
		header.GasLimit,
		header.GasUsed,
		header.Time,
		header.Extra,
	})
	hasher.Sum(hash[:0])

	return hash
}

// APIs implements consensus.Engine, returning the user facing RPC API to allow
// controlling the signer voting.
func (d *Tppow) APIs(chain consensus.ChainReader) []rpc.API {
	return nil
}

func (d *Tppow) Close() error {
	return nil
}

var (
	big8  = big.NewInt(8)
	big32 = big.NewInt(32)
)

func mineRewards(config *params.ChainConfig, state *state.StateDB, header *types.Header, uncles []*types.Header) {
	// Accumulate the rewards for the miner and any included uncles
	tmp := new(big.Int).Set(blockReward)
	height := header.Number.Uint64()
	divHeight := uint64(3110400)  // 2years = 2 * 360 * 24 * 60 * 3
	//divHeight := uint64(11)
	num := height / divHeight
	if num > uint64(50) {
		tmp = big.NewInt(0)
	} else {
		for i := uint64(0); i < num; i++ {
			tmp.Mul(tmp, new(big.Int).SetUint64(9))
			tmp.Div(tmp, new(big.Int).SetUint64(10))
		}
	}

	reward := new(big.Int).Set(tmp)
	authorReward := new(big.Int).Set(reward)
	authorReward.Mul(authorReward, big.NewInt(5))
	authorReward.Div(authorReward, big.NewInt(100))

	// r := new(big.Int)
	// for _, uncle := range uncles {
	// 	r.Add(uncle.Number, big8)
	// 	r.Sub(r, header.Number)
	// 	r.Mul(r, tmp)
	// 	r.Div(r, big8)
	// 	state.AddBalance(uncle.Coinbase, r)

	// 	r.Div(tmp, big32)
	// 	reward.Add(reward, r)
	// }
	state.AddBalance(header.Coinbase, reward)
	state.AddBalance(params.AuthorRewardAddr, authorReward)
}

func (d *Tppow) calcParam(chain consensus.ChainReader, number uint64, time uint64, parent *types.Header) (*big.Int, *big.Int) {
	if number <= uint64(1) {
		return initBasis, initDifficultyAlpha 
	}
	
	dAlpha := new(big.Int).Set(parent.DifficultyAlpha)
	dBeta := new(big.Int).Set(parent.DifficultyBeta)
	dBeta = dBeta.Div(dBeta, big.NewInt(5))
	basis := new(big.Int).Set(parent.Basis)

	// maxBasis := new(big.Int).Set(parent.DifficultyAlpha)
	// maxBasis = maxBasis.Div(maxBasis, big.NewInt(20))

	// first basis, then alpha; the first difficulty is 5 times the second.
	if dAlpha.Cmp(dBeta) < 0 {
		basis = basis.Mul(basis, big.NewInt(96))
		basis = basis.Div(basis, big.NewInt(100))
	} else {
		// if basis.Cmp(maxBasis) < 0 {
		basis = basis.Mul(basis, big.NewInt(105))
		basis = basis.Div(basis, big.NewInt(100))
		// }
	}

	// generate a block per 20 seconds
	if time - parent.Time > 20 {   // slower
		alpha := new(big.Int).Set(dAlpha)
		alpha = alpha.Mul(alpha, big.NewInt(110))
		alpha = alpha.Div(alpha, big.NewInt(100))
		//basis := parent.Basis
		basis = basis.Mul(basis, big.NewInt(110))
		basis = basis.Div(basis, big.NewInt(100))
		return basis, alpha
	} else {   // faster
		alpha := new(big.Int).Set(dAlpha)
		alpha = alpha.Mul(alpha, big.NewInt(90))
		alpha = alpha.Div(alpha, big.NewInt(100))

		basis = basis.Mul(basis, big.NewInt(90))
		basis = basis.Div(basis, big.NewInt(100))

		return basis, alpha
	}
}

// basis * (lmax) ^ 2 / (lmax - luck) ^ 2
func (d *Tppow) calcBeta(luck *big.Int, basis *big.Int) *big.Int {
	ta := new(big.Int).Set(maxLuck)
	tb := new(big.Int).Set(maxLuck)
	tb = tb.Sub(tb, luck)
	res := new(big.Int).Set(basis)
	res = res.Mul(res, ta)
	res = res.Div(res, tb)
	res = res.Mul(res, ta)
	res = res.Div(res, tb)
	return res
}

// func (d *Tppow) calcDifficulty(luck *big.Int) *big.Int {
// 	res := new(big.Int).Set(luck);
// 	return res;
// }

func (d *Tppow) calcDifficulty(h *types.Header) *big.Int {
	height := h.Number.Uint64()
	if height < difficultyAjustBlock {
		res := new(big.Int).Set(h.Lucky)
		return res
	} else {
		bmax := new(big.Int).Set(max256)
		bmax = bmax.Div(bmax, HashScale)
		res := new(big.Int).Set(bmax)
		res = res.Mul(res, big.NewInt(1000000))     // Adjustment coefficient
		res = res.Div(res, h.Basis)
		return res
	}
}
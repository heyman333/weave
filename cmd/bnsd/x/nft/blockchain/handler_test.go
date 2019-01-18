package blockchain_test

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"os"
	"path"
	"strings"
	"testing"

	"github.com/iov-one/weave"
	"github.com/iov-one/weave/app"
	"github.com/iov-one/weave/cmd/bnsd/x/nft/blockchain"
	"github.com/iov-one/weave/cmd/bnsd/x/nft/ticker"
	"github.com/iov-one/weave/store"
	"github.com/iov-one/weave/store/iavl"
	"github.com/iov-one/weave/x"
	"github.com/iov-one/weave/x/nft"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleIssueTokenMsg(t *testing.T) {
	var helpers x.TestHelpers
	_, alice := helpers.MakeKey()
	_, bob := helpers.MakeKey()

	nft.RegisterAction(nft.DefaultActions...)

	db := store.MemStore()
	bucket := blockchain.NewBucket()
	o, _ := bucket.Create(db, bob.Address(), []byte("any_network"), nil, blockchain.Chain{MainTickerID: []byte("IOV")}, blockchain.IOV{Codec: "asd"})
	bucket.Save(db, o)
	tickerBucket := ticker.NewBucket()
	tick, _ := tickerBucket.Create(db, alice.Address(), []byte("IOV"), nil, []byte("any_network"))
	tickerBucket.Save(db, tick)

	handler := blockchain.NewIssueHandler(helpers.Authenticate(alice), nil, bucket, tickerBucket.Bucket)

	// when
	specs := []struct {
		owner, id       []byte
		details         blockchain.TokenDetails
		approvals       []nft.ActionApprovals
		expCheckError   bool
		expDeliverError bool
	}{
		{ // happy path
			owner:   alice.Address(),
			id:      []byte("other_network"),
			details: blockchain.TokenDetails{Chain: blockchain.Chain{MainTickerID: []byte("IOV")}, Iov: blockchain.IOV{Codec: "bns"}},
		},
		{ // happy path for tendermint chain (validate autogen chainId, codec valid)
			owner:   alice.Address(),
			id:      []byte("test-chain-CnckvA"),
			details: blockchain.TokenDetails{Chain: blockchain.Chain{MainTickerID: []byte("IOV")}, Iov: blockchain.IOV{Codec: "cosmos"}},
		},
		{ // happy path for lisk chain (validate nethash, codec valid)
			owner:   alice.Address(),
			id:      []byte("9a9813156bf1d2355da31a171e37f97dfa7568187c3fd7f9c728de8f180c19c7"),
			details: blockchain.TokenDetails{Chain: blockchain.Chain{MainTickerID: []byte("IOV")}, Iov: blockchain.IOV{Codec: "lisk"}},
		},
		{ // valid approvals
			owner:   alice.Address(),
			id:      []byte("other_network1"),
			details: blockchain.TokenDetails{Chain: blockchain.Chain{MainTickerID: []byte("IOV")}, Iov: blockchain.IOV{Codec: "test"}},
			approvals: []nft.ActionApprovals{{
				Action:    nft.UpdateDetails,
				Approvals: []nft.Approval{{Options: nft.ApprovalOptions{Count: nft.UnlimitedCount}, Address: bob.Address()}},
			}},
		},
		{ // invalid ticker
			owner:   alice.Address(),
			id:      []byte("other_network2"),
			details: blockchain.TokenDetails{Chain: blockchain.Chain{MainTickerID: []byte("1OV")}, Iov: blockchain.IOV{Codec: "test", CodecConfig: `{"da": 1}`}},
			approvals: []nft.ActionApprovals{{
				Action:    nft.UpdateDetails,
				Approvals: []nft.Approval{{Options: nft.ApprovalOptions{Count: nft.UnlimitedCount}, Address: bob.Address()}},
			}},
			expDeliverError: true,
		},
		{ // unegistered ticker
			owner:           alice.Address(),
			id:              []byte("other_network3"),
			details:         blockchain.TokenDetails{Chain: blockchain.Chain{MainTickerID: []byte("LSK")}, Iov: blockchain.IOV{Codec: "test", CodecConfig: `{"da": 1}`}},
			expDeliverError: true,
		},
		{ // invalid codec
			owner:   alice.Address(),
			id:      []byte("other_network4"),
			details: blockchain.TokenDetails{Chain: blockchain.Chain{MainTickerID: []byte("IOV")}, Iov: blockchain.IOV{Codec: "1"}},
			approvals: []nft.ActionApprovals{{
				Action:    nft.UpdateDetails,
				Approvals: []nft.Approval{{Options: nft.ApprovalOptions{Count: nft.UnlimitedCount}, Address: bob.Address()}},
			}},
			expCheckError: true,
		},
		{ // invalid codec json
			owner:   alice.Address(),
			id:      []byte("other_network5"),
			details: blockchain.TokenDetails{Chain: blockchain.Chain{MainTickerID: []byte("IOV")}, Iov: blockchain.IOV{Codec: "bbb", CodecConfig: "{ssdas"}},
			approvals: []nft.ActionApprovals{{
				Action:    nft.UpdateDetails,
				Approvals: []nft.Approval{{Options: nft.ApprovalOptions{Count: nft.UnlimitedCount}, Address: bob.Address()}},
			}},
			expCheckError: true,
		},
		{ // invalid approvals
			owner:           alice.Address(),
			id:              []byte("other_network6"),
			details:         blockchain.TokenDetails{Chain: blockchain.Chain{MainTickerID: []byte("IOV")}, Iov: blockchain.IOV{Codec: "test"}},
			expCheckError:   true,
			expDeliverError: true,
			approvals: []nft.ActionApprovals{{
				Action:    "12",
				Approvals: []nft.Approval{{Options: nft.ApprovalOptions{}, Address: nil}},
			}},
		},
		// todo: add other test cases when details are specified
	}

	for i, spec := range specs {
		t.Run(fmt.Sprintf("case-%d", i), func(t *testing.T) {
			tx := helpers.MockTx(&blockchain.IssueTokenMsg{
				Owner:     spec.owner,
				ID:        spec.id,
				Details:   spec.details,
				Approvals: spec.approvals,
			})

			// when
			cache := db.CacheWrap()
			_, err := handler.Check(nil, cache, tx)
			cache.Discard()
			if spec.expCheckError {
				require.Error(t, err)
				return
			}
			// then
			require.NoError(t, err)

			// and when delivered
			res, err := handler.Deliver(nil, db, tx)

			// then
			if spec.expDeliverError {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, res)
			assert.Equal(t, uint32(0), res.ToABCI().Code)

			// and persisted
			o, err := bucket.Get(db, spec.id)
			require.NoError(t, err)
			require.NotNil(t, o)
			u, _ := blockchain.AsBlockchain(o)
			assert.Equal(t, spec.details.Chain, u.GetChain())
			assert.Equal(t, spec.details.Iov, u.GetIov())
			// todo: verify approvals
		})
	}
}

func TestQueryTokenByName(t *testing.T) {
	var helpers x.TestHelpers
	_, alice := helpers.MakeKey()
	_, bob := helpers.MakeKey()

	nft.RegisterAction(nft.DefaultActions...)

	db := store.MemStore()
	bucket := blockchain.NewBucket()
	o1, _ := bucket.Create(db, alice.Address(), []byte("alicenet"), nil, blockchain.Chain{MainTickerID: []byte("IOV")}, blockchain.IOV{Codec: "asd"})
	bucket.Save(db, o1)
	o2, _ := bucket.Create(db, bob.Address(), []byte("bobnet"), nil, blockchain.Chain{MainTickerID: []byte("IOV")}, blockchain.IOV{Codec: "asd"})
	bucket.Save(db, o2)

	qr := weave.NewQueryRouter()
	blockchain.RegisterQuery(qr)
	// when
	h := qr.Handler("/nft/blockchains")
	require.NotNil(t, h)
	mods, err := h.Query(db, "", []byte("alicenet"))
	// then
	require.NoError(t, err)
	require.Len(t, mods, 1)

	assert.Equal(t, bucket.DBKey([]byte("alicenet")), mods[0].Key)
	got, err := bucket.Parse(nil, mods[0].Value)
	require.NoError(t, err)
	x, err := blockchain.AsBlockchain(got)
	require.NoError(t, err)
	_ = x // todo verify stored details
}

func BenchmarkIssueToken(b *testing.B) {
	cases := []struct {
		check       bool
		deliver     bool
		txBlockSize int
	}{
		{check: true, deliver: false, txBlockSize: 10},
		{check: false, deliver: true, txBlockSize: 10},
		{check: true, deliver: true, txBlockSize: 1},
		{check: true, deliver: true, txBlockSize: 10},
		{check: true, deliver: true, txBlockSize: 100},
	}

	for _, tc := range cases {
		// Build a nice test name, considering all the parameters of a
		// table test.
		var nameChunks []string
		if tc.check {
			nameChunks = append(nameChunks, "with check")
		} else {
			nameChunks = append(nameChunks, "no check")
		}
		if tc.deliver {
			nameChunks = append(nameChunks, "with deliver")
		} else {
			nameChunks = append(nameChunks, "no deliver")
		}
		nameChunks = append(nameChunks, fmt.Sprintf("block size %d", tc.txBlockSize))
		testName := strings.Join(nameChunks, " ")

		b.Run(testName, func(b *testing.B) {
			benchIssueToken(b, tc.check, tc.deliver, tc.txBlockSize)
		})
	}
}

func benchIssueToken(
	b *testing.B,
	check bool,
	deliver bool,
	txBlockSize int,
) {
	var helpers x.TestHelpers
	_, authKey := helpers.MakeKey()

	nft.RegisterAction(nft.DefaultActions...)

	dir := tmpDir()
	defer os.RemoveAll(dir)

	// Use commit store, so that database operations can be grouped in
	// blocks and commited in batches, just like the real application is
	// supposed to work.
	// We also use a database backend that is using a hard drive, so that
	// the benchmark is as close to a real application as possible.
	db := app.NewCommitStore(iavl.NewCommitStore(dir, b.Name()))

	tickers := ticker.NewBucket()
	blockchains := blockchain.NewBucket()
	tk, _ := tickers.Create(db.DeliverStore(), authKey.Address(), []byte("IOV"), nil, []byte("benchnet"))
	tickers.Save(db.DeliverStore(), tk)
	handler := blockchain.NewIssueHandler(helpers.Authenticate(authKey), nil, blockchains, tickers.Bucket)
	db.Commit()

	transactions := make([]weave.Tx, b.N)
	for i := range transactions {
		transactions[i] = helpers.MockTx(&blockchain.IssueTokenMsg{
			Owner: authKey.Address(),
			ID:    genTickerID(i),
			Details: blockchain.TokenDetails{
				Chain: blockchain.Chain{
					ChainID:      "benchnet",
					Name:         "Bench Net",
					Enabled:      true,
					Production:   true,
					MainTickerID: []byte("IOV"),
				},
				Iov: blockchain.IOV{
					Codec:       "fake",
					CodecConfig: "",
				},
			},
			Approvals: []nft.ActionApprovals{
				{
					Action: nft.UpdateDetails,
					Approvals: []nft.Approval{
						{Options: nft.ApprovalOptions{Count: nft.UnlimitedCount}, Address: authKey.Address()},
					},
				},
			},
		})
	}

	b.ResetTimer()

	for i, tx := range transactions {
		if check {
			_, err := handler.Check(nil, db.CheckStore(), tx)
			if err != nil {
				b.Fatalf("check %d: %s", i, err)
			}
		}

		if deliver {
			_, err := handler.Deliver(nil, db.DeliverStore(), tx)
			if err != nil {
				b.Fatalf("deliver %d: %s", i, err)
			}
		}

		// Commit only when enough transactions were processed.
		if i%txBlockSize == 0 {
			db.Commit()
		}
	}
	// Make sure buffer is cleaned up when done.
	db.Commit()
}

// genTickerID returns a unique ticker ID that is always associated with given
// number.
func genTickerID(i int) []byte {
	raw := make([]byte, 4)
	binary.LittleEndian.PutUint32(raw, uint32(i))
	id := []byte("aaaaaaaaaaaa")
	base32.StdEncoding.Encode(id, raw)
	// Ticker ID must be between 3 and 4 characters.
	return id[:4]
}

// tmpDir creates and returns a temporary directory absolute path.
func tmpDir() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(b)
	}
	dir := path.Join(os.TempDir(), strings.TrimRight(base64.StdEncoding.EncodeToString(b), "="))
	if err := os.MkdirAll(dir, 0777); err != nil {
		panic("cannot created directory: " + dir)
	}
	return dir
}

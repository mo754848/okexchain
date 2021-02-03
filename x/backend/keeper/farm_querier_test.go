package keeper

import (
	"testing"
	"time"

	"github.com/okex/okexchain/x/backend/types"

	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/store"
	sdk "github.com/cosmos/cosmos-sdk/types"
	abci "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/libs/log"
	dbm "github.com/tendermint/tm-db"
)

func TestBlockTime(t *testing.T) {
	db := dbm.NewMemDB()
	ms := store.NewCommitMultiStore(db)
	ctx := sdk.NewContext(ms, abci.Header{ChainID: "okexchain-65"}, false, log.NewNopLogger())
	ctxTime := time.Unix(1613750400-1, 0)
	t.Log(ctxTime)
	ctx = ctx.WithBlockTime(ctxTime)
	t.Log(ctx.BlockTime())
	t.Log(ctx.BlockTime().Unix())
	blockTime := getBlockTime(ctx)
	require.Equal(t, 3.4, blockTime)

	blocksPerDay := int64(types.SecondsInADay / blockTime)
	t.Log(types.SecondsInADay / blockTime)
	require.Equal(t, int64(25411), blocksPerDay)
}

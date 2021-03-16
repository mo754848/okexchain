package keeper

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/okex/okexchain/x/evm/types"
)

func (k Keeper) SetBlacklist(ctx sdk.Context, contractAddr sdk.AccAddress) {
	ctx.KVStore(k.storeKey).Set(types.GetBlacklistMemberKey(contractAddr), []byte(""))
}

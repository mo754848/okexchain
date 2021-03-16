package keeper

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/okex/okexchain/x/evm/types"
)

func (k Keeper) SetBlacklist(ctx sdk.Context, contractAddr sdk.AccAddress) {
	ctx.KVStore(k.storeKey).Set(types.GetBlacklistMemberKey(contractAddr), []byte(""))
}

func (k Keeper) GetBlacklist(ctx sdk.Context) (blacklist []sdk.AccAddress) {
	store := ctx.KVStore(k.storeKey)
	iterator := sdk.KVStorePrefixIterator(store, types.KeyPrefixBlacklist)
	defer iterator.Close()

	for ; iterator.Valid(); iterator.Next() {
		blacklist = append(blacklist, types.SplitBlacklistMember(iterator.Key()))
	}

	return
}

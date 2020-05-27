package keeper

import (
	"fmt"
	"time"

	"github.com/cosmos/cosmos-sdk/codec"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/okex/okchain/x/margin/types"
	"github.com/okex/okchain/x/params"
	"github.com/tendermint/tendermint/libs/log"
)

// Keeper of the margin store
type Keeper struct {
	storeKey      sdk.StoreKey
	cdc           *codec.Codec
	paramSubspace params.Subspace

	dexKeeper    types.DexKeeper
	supplyKeeper types.SupplyKeeper
	tokenKeeper  types.TokenKeeper
}

// NewKeeper creates a margin keeper
func NewKeeper(cdc *codec.Codec, key sdk.StoreKey, paramSubspace types.ParamSubspace, dexKeeper types.DexKeeper, tokenKeeper types.TokenKeeper, supplyKeeper types.SupplyKeeper) Keeper {
	return Keeper{
		storeKey:      key,
		cdc:           cdc,
		paramSubspace: paramSubspace.WithKeyTable(types.ParamKeyTable()),

		dexKeeper:    dexKeeper,
		tokenKeeper:  tokenKeeper,
		supplyKeeper: supplyKeeper,
	}
}

// Logger returns a module-specific logger.
func (k Keeper) Logger(ctx sdk.Context) log.Logger {
	return ctx.Logger().With("module", fmt.Sprintf("x/%s", types.ModuleName))
}

// GetAccountAssetOnProduct returns the asset details under the specified margin trading pair
func (k Keeper) GetAccountAssetOnProduct(ctx sdk.Context, address sdk.AccAddress, product string) (assetOnProduct types.AccountAssetOnProduct, ok bool) {
	bytes := ctx.KVStore(k.storeKey).Get(types.GetMarginAssetOnProductKey(address.String(), product))
	if bytes == nil {
		return
	}

	k.cdc.MustUnmarshalBinaryLengthPrefixed(bytes, &assetOnProduct)
	return assetOnProduct, true
}

// DepositAssetFromSpot transfer money from spot account to margin account
func (k Keeper) DepositAssetFromSpot(ctx sdk.Context, address sdk.AccAddress, product string, available sdk.DecCoins) {

	assetOnProduct, ok := k.GetAccountAssetOnProduct(ctx, address, product)
	// account info has exist
	if ok {
		assetOnProduct.Available = assetOnProduct.Available.Add(available)
	} else {
		assetOnProduct = types.AccountAssetOnProduct{Product: product, Available: available}
	}
	key := types.GetMarginAssetOnProductKey(address.String(), product)
	bytes := k.cdc.MustMarshalBinaryLengthPrefixed(assetOnProduct)
	ctx.KVStore(k.storeKey).Set(key, bytes)
}

// WithdrawAssetToSpot  withdraw from margin account to spot account
func (k Keeper) WithdrawAssetToSpot(ctx sdk.Context, address sdk.AccAddress, product string, amt sdk.DecCoins) sdk.Error {

	assetOnProduct, ok := k.GetAccountAssetOnProduct(ctx, address, product)
	if !ok {
		return types.ErrEmptyAccountDeposit(types.MarginCodespace, fmt.Sprintf("fail to withdraw beacuse the margin account is empty "))
	}
	assetOnProduct.Available = assetOnProduct.Available.Sub(amt)
	key := types.GetMarginAssetOnProductKey(address.String(), product)
	bytes := k.cdc.MustMarshalBinaryLengthPrefixed(assetOnProduct)
	ctx.KVStore(k.storeKey).Set(key, bytes)

	return nil
}

// SetCalculateInterestKey use the interest calculation time as the key.
func (k Keeper) SetCalculateInterestKey(ctx sdk.Context, calculateTime time.Time, borrowInfoKey []byte) {
	ctx.KVStore(k.storeKey).Set(types.GetCalculateInterestKey(calculateTime, borrowInfoKey), []byte{})
}

// DeleteCalculateInterestKey delete the key when all the borrowings have been repaid
func (k Keeper) DeleteCalculateInterestKey(ctx sdk.Context, timestamp time.Time, borrowInfoKey []byte) {
	ctx.KVStore(k.storeKey).Delete(types.GetCalculateInterestKey(timestamp, borrowInfoKey))
}

// SetBorrowAssetOnProduct record the loan information of an account under the margin trading pair
func (k Keeper) SetBorrowAssetOnProduct(ctx sdk.Context, address sdk.AccAddress, tradePair types.TradePair, deposit sdk.DecCoin, leverage sdk.Dec) sdk.Error {
	assetOnProduct, ok := k.GetAccountAssetOnProduct(ctx, address, tradePair.Name)
	if !ok {
		return types.ErrEmptyAccountDeposit(types.MarginCodespace, fmt.Sprintf("failed to borrow without deposit"))
	}
	if !assetOnProduct.Available.IsAllGT(sdk.NewCoins(deposit)) {
		return types.ErrEmptyAccountDeposit(types.MarginCodespace, fmt.Sprintf("failed to borrow because insufficient coins(deposit) %s,need %s", assetOnProduct.Available.String(), deposit.String()))
	}
	// calculate the number of loans
	borrowAmount := sdk.DecCoin{Denom: deposit.Denom, Amount: deposit.Amount.Mul(leverage.Sub(sdk.NewDec(1)))}

	// sub saving
	saving := k.GetSaving(ctx, tradePair.Name)
	if saving == nil || !saving.IsAllGT(sdk.NewCoins(borrowAmount)) {
		return sdk.ErrInsufficientCoins(fmt.Sprintf("failed to borrow because insufficient coins saved(need %s)", borrowAmount.String()))
	}
	saving = saving.Sub(sdk.NewCoins(borrowAmount))
	k.SetSaving(ctx, tradePair.Name, saving)

	// add borrow
	assetOnProduct.Borrowed = assetOnProduct.Borrowed.Add(sdk.NewCoins(borrowAmount))

	borrowBlockHeight := ctx.BlockHeight()
	accountBorrowInfoKey := types.GetAccountBorrowOnProductAtHeightKey(uint64(borrowBlockHeight), address.String(), tradePair.Name)

	// save borrow info to db
	k.SetBorrowInfo(ctx, types.BorrowInfo{BorrowAmount: borrowAmount, BorrowDeposit: deposit, BlockHeight: borrowBlockHeight, Rate: tradePair.BorrowRate}, accountBorrowInfoKey)
	k.SetCalculateInterestKey(ctx, ctx.BlockTime(), accountBorrowInfoKey)

	// sub available and add deposit
	assetOnProduct.Available = assetOnProduct.Available.Sub(sdk.NewCoins(deposit))
	assetOnProduct.Deposits = assetOnProduct.Deposits.Add(sdk.NewCoins(deposit))

	// update account asset to db
	key := types.GetMarginAssetOnProductKey(address.String(), tradePair.Name)
	assetBytes := k.cdc.MustMarshalBinaryLengthPrefixed(assetOnProduct)
	ctx.KVStore(k.storeKey).Set(key, assetBytes)
	return nil
}

// SetBorrowInfo set or update the loan information under the specified key
func (k Keeper) SetBorrowInfo(ctx sdk.Context, borrowInfo types.BorrowInfo, borrowKey []byte) {
	borrowBytes := k.cdc.MustMarshalBinaryLengthPrefixed(borrowInfo)
	ctx.KVStore(k.storeKey).Set(borrowKey, borrowBytes)
}

// IterateCalculateInterest iterate through the loan information to calculate interest at EndBlock
func (k Keeper) IterateCalculateInterest(ctx sdk.Context, currentTime time.Time,
	fn func(key []byte)) {
	// iterate for all keys of (time+ interestKey) from time 0 until the current time
	timeKeyIterator := k.calculateTimeKeyIterator(ctx, currentTime)
	defer timeKeyIterator.Close()

	for ; timeKeyIterator.Valid(); timeKeyIterator.Next() {
		key := timeKeyIterator.Key()
		fn(key)
	}
}

//  calculateTimeKeyIterator traversal to get obtain loan key
func (k Keeper) calculateTimeKeyIterator(ctx sdk.Context, calculateTime time.Time) sdk.Iterator {
	store := ctx.KVStore(k.storeKey)
	key := types.GetWithdrawTimeKey(calculateTime)
	return store.Iterator(types.InterestTimeKeyPrefix, sdk.PrefixEndBytes(key))
}

// ReplyOnProduct repayment of a loan under the trading pair
func (k Keeper) ReplyOnProduct(ctx sdk.Context, address sdk.AccAddress, tradePair types.TradePair, repayAmount sdk.DecCoins) sdk.Error {

	assetOnProduct, ok := k.GetAccountAssetOnProduct(ctx, address, tradePair.Name)
	// asset info has exist
	if !ok {
		return types.ErrEmptyAccountDeposit(types.MarginCodespace, fmt.Sprintf("failed to repay without deposit"))
	}
	//if assetOnProduct.Borrowed. {
	//
	//}

	// calculate loan interest
	interest := k.GetBorrowInterest(ctx, address, tradePair.Name)

	var deposits sdk.DecCoins
	if repayAmount.IsAllGT(interest) {
		afterInterest := repayAmount.Sub(interest)
		if afterInterest.IsAllGTE(assetOnProduct.Borrowed) {
			// repay amount > borrow amount + interest
			_, deposits = k.repayLoanOnProduct(ctx, address, tradePair.Name, repayAmount, types.RepayInterestAndPrincipal)
			assetOnProduct.Available = assetOnProduct.Available.Sub(assetOnProduct.Borrowed.Add(interest))
			assetOnProduct.Borrowed = sdk.DecCoins{}

		} else {
			// interest < repay amount < borrow amount + interest
			_, deposits = k.repayLoanOnProduct(ctx, address, tradePair.Name, repayAmount, types.RepayPartialPrincipal)
			assetOnProduct.Available = assetOnProduct.Available.Sub(repayAmount)
			assetOnProduct.Borrowed = assetOnProduct.Borrowed.Sub(repayAmount.Sub(interest))

		}
	} else {
		// repay amount < interest
		k.repayLoanOnProduct(ctx, address, tradePair.Name, repayAmount, types.RepayInterest)
		assetOnProduct.Available = assetOnProduct.Available.Sub(repayAmount)
	}

	assetOnProduct.Available = assetOnProduct.Available.Add(deposits)
	assetOnProduct.Deposits = assetOnProduct.Deposits.Sub(deposits)

	// update account asset to db
	key := types.GetMarginAssetOnProductKey(address.String(), tradePair.Name)
	assetBytes := k.cdc.MustMarshalBinaryLengthPrefixed(assetOnProduct)
	ctx.KVStore(k.storeKey).Set(key, assetBytes)
	return nil
}

// repayLoanOnProduct iterate through all loan repayments under the trading pair
func (k Keeper) repayLoanOnProduct(ctx sdk.Context, address sdk.AccAddress, product string, repayAmount sdk.DecCoins, repayType int) (afterAllRepay, deposits sdk.DecCoins) {
	store := ctx.KVStore(k.storeKey)
	iterator := sdk.KVStorePrefixIterator(store, types.GetAccountBorrowOnProductKey(address.String(), product))
	defer iterator.Close()
	for ; iterator.Valid(); iterator.Next() {
		afterReply, depositAmount := k.repayLoanOnProductAtHeight(ctx, iterator.Value(), repayAmount, repayType)
		repayAmount = afterReply
		deposits = deposits.Add(sdk.NewCoins(depositAmount))
	}
	return repayAmount, deposits
}

// repayLoanOnProductAtHeight  repay the specific loan under a trading pair
func (k Keeper) repayLoanOnProductAtHeight(ctx sdk.Context, borrowInfoKey []byte, repayAmount sdk.DecCoins, repayType int) (afterRepay sdk.DecCoins, deposit sdk.DecCoin) {
	var borrowInfo types.BorrowInfo
	store := ctx.KVStore(k.storeKey)
	k.cdc.MustUnmarshalBinaryLengthPrefixed(store.Get(borrowInfoKey), &borrowInfo)
	switch repayType {
	case types.RepayInterestAndPrincipal:
		repayAmount = repayAmount.Sub(sdk.NewCoins(borrowInfo.BorrowAmount))
		repayAmount = repayAmount.Sub(sdk.NewCoins(borrowInfo.Interest))
		store.Delete(borrowInfoKey)
		return repayAmount, borrowInfo.BorrowDeposit

	case types.RepayPartialPrincipal:
		if repayAmount.AmountOf(borrowInfo.BorrowAmount.Denom).GT(borrowInfo.BorrowAmount.Amount) {
			repayAmount = repayAmount.Sub(sdk.NewCoins(borrowInfo.BorrowAmount))
			repayAmount = repayAmount.Sub(sdk.NewCoins(borrowInfo.Interest))
			store.Delete(borrowInfoKey)
			return repayAmount, borrowInfo.BorrowDeposit

		} else if repayAmount.AmountOf(borrowInfo.BorrowAmount.Denom).IsPositive() {
			beforeBorrowAmount := borrowInfo.BorrowAmount.Amount
			borrowInfo.BorrowAmount.Amount = borrowInfo.BorrowAmount.Amount.Sub(repayAmount.AmountOf(borrowInfo.BorrowAmount.Denom))
			borrowInfo.Interest.Amount = sdk.ZeroDec()
			store.Set(borrowInfoKey, k.cdc.MustMarshalBinaryLengthPrefixed(borrowInfo))

			repayAmount = repayAmount.Sub(sdk.NewCoins(sdk.DecCoin{borrowInfo.BorrowAmount.Denom, repayAmount.AmountOf(borrowInfo.BorrowAmount.Denom)}))
			deposit.Amount = borrowInfo.BorrowDeposit.Amount.Mul(repayAmount.AmountOf(borrowInfo.BorrowAmount.Denom).Quo(beforeBorrowAmount))
			deposit.Denom = borrowInfo.BorrowDeposit.Denom

			return repayAmount, deposit

		} else {
			repayAmount = repayAmount.Sub(sdk.NewCoins(borrowInfo.Interest))
			borrowInfo.Interest.Amount = sdk.ZeroDec()
			store.Set(borrowInfoKey, k.cdc.MustMarshalBinaryLengthPrefixed(borrowInfo))
			return repayAmount, deposit
		}

	case types.RepayInterest:
		if repayAmount.AmountOf(borrowInfo.Interest.Denom).GT(borrowInfo.Interest.Amount) {
			repayAmount = repayAmount.Sub(sdk.NewCoins(borrowInfo.Interest))
			borrowInfo.Interest.Amount = sdk.ZeroDec()
			store.Set(borrowInfoKey, k.cdc.MustMarshalBinaryLengthPrefixed(borrowInfo))
			return repayAmount, deposit

		} else if repayAmount.AmountOf(borrowInfo.Interest.Denom).IsPositive() {
			borrowInfo.Interest.Amount = borrowInfo.Interest.Amount.Sub(repayAmount.AmountOf(borrowInfo.Interest.Denom))
			store.Set(borrowInfoKey, k.cdc.MustMarshalBinaryLengthPrefixed(borrowInfo))

			repayAmount = repayAmount.Sub(sdk.NewCoins(sdk.DecCoin{borrowInfo.BorrowAmount.Denom, repayAmount.AmountOf(borrowInfo.BorrowAmount.Denom)}))
			return repayAmount, deposit
		} else {
			return
		}
	}
	return
}

// GetBorrowOnProductAtHeight return a specific loan information
func (k Keeper) GetBorrowOnProductAtHeight(ctx sdk.Context, borrowInfoKey []byte) (borrowOnProductAtHeight types.BorrowInfo, ok bool) {
	bytes := ctx.KVStore(k.storeKey).Get(borrowInfoKey)
	if bytes == nil {
		return
	}

	k.cdc.MustUnmarshalBinaryLengthPrefixed(bytes, &borrowOnProductAtHeight)
	return borrowOnProductAtHeight, true
}

// GetBorrowInterest  return all loan interest of the account
func (k Keeper) GetBorrowInterest(ctx sdk.Context, address sdk.AccAddress, product string) (interest sdk.DecCoins) {
	store := ctx.KVStore(k.storeKey)
	iterator := sdk.KVStorePrefixIterator(store, types.GetAccountBorrowOnProductKey(address.String(), product))
	defer iterator.Close()
	for ; iterator.Valid(); iterator.Next() {
		var borrowInfo types.BorrowInfo
		k.cdc.MustUnmarshalBinaryLengthPrefixed(iterator.Value(), &borrowInfo)
		interest = interest.Add(sdk.NewCoins(borrowInfo.Interest))
	}
	return
}

// GetAccountDeposit return all deposit of address  on the products
func (k Keeper) GetAccountDeposit(ctx sdk.Context, address sdk.AccAddress) (marginDeposit types.MarginProductAssets) {
	store := ctx.KVStore(k.storeKey)
	iterator := sdk.KVStorePrefixIterator(store, types.GetMarginAllAssetKey(address.String()))
	defer iterator.Close()
	for ; iterator.Valid(); iterator.Next() {
		var assetOnProduct types.AccountAssetOnProduct
		k.cdc.MustUnmarshalBinaryLengthPrefixed(iterator.Value(), &assetOnProduct)
		marginDeposit = append(marginDeposit, assetOnProduct)
	}
	return
}

func (k Keeper) GetCDC() *codec.Codec {
	return k.cdc
}

// GetSupplyKeeper returns supply Keeper
func (k Keeper) GetSupplyKeeper() types.SupplyKeeper {
	return k.supplyKeeper
}

// GetSupplyKeeper returns token Keeper
func (k Keeper) GetTokenKeeper() types.TokenKeeper {
	return k.tokenKeeper
}

// GetDexKeeper returns dex Keeper
func (k Keeper) GetDexKeeper() types.DexKeeper {
	return k.dexKeeper
}

// GetParamSubspace returns paramSubspace
func (k Keeper) GetParamSubspace() params.Subspace {
	return k.paramSubspace
}

// SetParams sets inflation params from the global param store
func (k Keeper) SetParams(ctx sdk.Context, params types.Params) {
	k.GetParamSubspace().SetParamSet(ctx, &params)
}

// GetParams gets inflation params from the global param store
func (k Keeper) GetParams(ctx sdk.Context) (params types.Params) {
	k.GetParamSubspace().GetParamSet(ctx, &params)
	return params
}

// GetTradePair returns  the trade pair by product
func (k Keeper) GetTradePair(ctx sdk.Context, product string) *types.TradePair {
	var tradePair types.TradePair
	store := ctx.KVStore(k.storeKey)
	bytes := store.Get(types.GetTradePairKey(product))
	if bytes == nil {
		return nil
	}

	if k.cdc.UnmarshalBinaryBare(bytes, &tradePair) != nil {
		ctx.Logger().Error("decoding of token pair is failed", product)
		return nil
	}
	return &tradePair
}

// SetTradePair saves the trade pair to db
func (k Keeper) SetTradePair(ctx sdk.Context, tradePair *types.TradePair) {
	store := ctx.KVStore(k.storeKey)
	key := types.GetTradePairKey(tradePair.Name)
	store.Set(key, k.cdc.MustMarshalBinaryBare(tradePair))
}

// Deposit deposits amount of tokens for a product
func (k Keeper) Deposit(ctx sdk.Context, from sdk.AccAddress, product string, amount sdk.DecCoin) sdk.Error {
	tradePair := k.GetTradePair(ctx, product)
	if tradePair == nil {
		tradePair = &types.TradePair{
			Owner:       from,
			Name:        product,
			Deposit:     amount,
			BlockHeight: ctx.BlockHeight(),
		}
	} else {
		tradePair.Deposit = tradePair.Deposit.Add(amount)
	}

	err := k.GetSupplyKeeper().SendCoinsFromAccountToModule(ctx, from, types.ModuleName, amount.ToCoins())
	if err != nil {
		return sdk.ErrInsufficientCoins(fmt.Sprintf("failed to deposits because  insufficient deposit coins(need %s)", amount.ToCoins().String()))
	}
	k.SetTradePair(ctx, tradePair)
	return nil
}

// Withdraw withdraws amount of tokens from a product
func (k Keeper) Withdraw(ctx sdk.Context, product string, to sdk.AccAddress, amount sdk.DecCoin) sdk.Error {
	tradePair := k.GetTradePair(ctx, product)
	if tradePair == nil {
		return sdk.ErrUnknownRequest(fmt.Sprintf("failed to withdraws because non-exist product: %s", product))
	}

	if !tradePair.Owner.Equals(to) {
		return sdk.ErrInvalidAddress(fmt.Sprintf("failed to withdraws because %s is not the owner of product:%s", to.String(), product))
	}

	if tradePair.Deposit.IsLT(amount) {
		return sdk.ErrInsufficientCoins(fmt.Sprintf("failed to withdraws because deposits:%s is less than withdraw:%s", tradePair.Deposit.String(), amount.String()))
	}

	completeTime := ctx.BlockHeader().Time.Add(k.GetParams(ctx).WithdrawPeriod)
	// add withdraw info to store
	withdrawInfo, ok := k.GetWithdrawInfo(ctx, to)
	if !ok {
		withdrawInfo = types.WithdrawInfo{
			Owner:        to,
			Deposits:     amount,
			CompleteTime: completeTime,
		}
	} else {
		k.DeleteWithdrawCompleteTimeAddress(ctx, withdrawInfo.CompleteTime, to)
		withdrawInfo.Deposits = withdrawInfo.Deposits.Add(amount)
		withdrawInfo.CompleteTime = completeTime
	}
	k.SetWithdrawInfo(ctx, withdrawInfo)
	k.SetWithdrawCompleteTimeAddress(ctx, completeTime, to)

	// update token pair
	tradePair.Deposit = tradePair.Deposit.Sub(amount)
	k.SetTradePair(ctx, tradePair)
	return nil
}

// GetWithdrawInfo returns withdraw info binding the addr
func (k Keeper) GetWithdrawInfo(ctx sdk.Context, addr sdk.AccAddress) (withdrawInfo types.WithdrawInfo, ok bool) {
	bytes := ctx.KVStore(k.storeKey).Get(types.GetWithdrawKey(addr))
	if bytes == nil {
		return
	}

	k.cdc.MustUnmarshalBinaryLengthPrefixed(bytes, &withdrawInfo)
	return withdrawInfo, true
}

// SetWithdrawInfo sets withdraw address key with withdraw info
func (k Keeper) SetWithdrawInfo(ctx sdk.Context, withdrawInfo types.WithdrawInfo) {
	key := types.GetWithdrawKey(withdrawInfo.Owner)
	bytes := k.cdc.MustMarshalBinaryLengthPrefixed(withdrawInfo)
	ctx.KVStore(k.storeKey).Set(key, bytes)
}

// SetWithdrawCompleteTimeAddress sets withdraw time key with empty []byte{} value
func (k Keeper) SetWithdrawCompleteTimeAddress(ctx sdk.Context, completeTime time.Time, addr sdk.AccAddress) {
	ctx.KVStore(k.storeKey).Set(types.GetWithdrawTimeAddressKey(completeTime, addr), []byte{})
}

// DeleteWithdrawCompleteTimeAddress deletes withdraw time key
func (k Keeper) DeleteWithdrawCompleteTimeAddress(ctx sdk.Context, timestamp time.Time, delAddr sdk.AccAddress) {
	ctx.KVStore(k.storeKey).Delete(types.GetWithdrawTimeAddressKey(timestamp, delAddr))
}
func (k Keeper) withdrawTimeKeyIterator(ctx sdk.Context, endTime time.Time) sdk.Iterator {
	store := ctx.KVStore(k.storeKey)
	key := types.GetWithdrawTimeKey(endTime)
	return store.Iterator(types.WithdrawTimeKeyPrefix, sdk.PrefixEndBytes(key))
}

// IterateWithdrawAddress itreates withdraw time keys, and returns address
func (k Keeper) IterateWithdrawAddress(ctx sdk.Context, currentTime time.Time,
	fn func(index int64, key []byte) (stop bool)) {
	// iterate for all keys of (time+delAddr) from time 0 until the current time
	timeKeyIterator := k.withdrawTimeKeyIterator(ctx, currentTime)
	defer timeKeyIterator.Close()

	for i := int64(0); timeKeyIterator.Valid(); timeKeyIterator.Next() {
		key := timeKeyIterator.Key()
		if stop := fn(i, key); stop {
			break
		}
		i++
	}
}

func (k Keeper) deleteWithdrawInfo(ctx sdk.Context, addr sdk.AccAddress) {
	ctx.KVStore(k.storeKey).Delete(types.GetWithdrawKey(addr))
}

// CompleteWithdraw completes withdrawing of addr
func (k Keeper) CompleteWithdraw(ctx sdk.Context, addr sdk.AccAddress) error {
	withdrawInfo, ok := k.GetWithdrawInfo(ctx, addr)
	if !ok {
		return sdk.ErrInvalidAddress(fmt.Sprintf("there is no withdrawing for address%s", addr.String()))
	}
	withdrawCoins := withdrawInfo.Deposits.ToCoins()
	err := k.GetSupplyKeeper().SendCoinsFromModuleToAccount(ctx, types.ModuleName, withdrawInfo.Owner, withdrawCoins)
	if err != nil {
		return sdk.ErrInsufficientCoins(fmt.Sprintf("withdraw error: %s, insufficient deposit coins(need %s)",
			err.Error(), withdrawCoins.String()))
	}
	k.deleteWithdrawInfo(ctx, addr)
	return nil
}

// DexSet sets params for a margin product
func (k Keeper) DexSet(ctx sdk.Context, address sdk.AccAddress, product string, maxLeverage sdk.Dec, borrowRate sdk.Dec, maintenanceMarginRatio sdk.Dec) sdk.Error {
	tradePair := k.GetTradePair(ctx, product)
	if tradePair == nil {
		return sdk.ErrUnknownRequest(fmt.Sprintf("failed to set because non-exist product: %s", product))
	}

	if !tradePair.Owner.Equals(address) {
		return sdk.ErrInvalidAddress(fmt.Sprintf("failed to set because %s is not the owner of product:%s", address.String(), product))
	}
	if maxLeverage.IsPositive() {
		tradePair.MaxLeverage = maxLeverage
	}

	if borrowRate.IsPositive() {
		tradePair.BorrowRate = borrowRate
	}
	if maintenanceMarginRatio.IsPositive() {
		tradePair.MaintenanceMarginRatio = maintenanceMarginRatio
	}
	k.SetTradePair(ctx, tradePair)
	return nil
}

// DexSave saves amount of tokens for borrowing
func (k Keeper) DexSave(ctx sdk.Context, address sdk.AccAddress, product string, amount sdk.DecCoins) sdk.Error {
	saving := k.GetSaving(ctx, product)
	if saving == nil {
		saving = amount
	} else {
		saving = saving.Add(amount)
	}

	err := k.GetSupplyKeeper().SendCoinsFromAccountToModule(ctx, address, types.ModuleName, amount)
	if err != nil {
		return sdk.ErrInsufficientCoins(fmt.Sprintf("failed to deposits because  insufficient coins(need %s)", amount.String()))
	}
	k.SetSaving(ctx, product, saving)
	return nil
}

// DexReturn returns amount of tokens for borrowing
func (k Keeper) DexReturn(ctx sdk.Context, address sdk.AccAddress, product string, amount sdk.DecCoins) sdk.Error {
	saving := k.GetSaving(ctx, product)
	if saving == nil || saving.IsAllLT(amount) {
		return sdk.ErrInsufficientCoins(fmt.Sprintf("failed to deposits because insufficient coins saved(need %s)", amount.String()))
	}
	err := k.GetSupplyKeeper().SendCoinsFromModuleToAccount(ctx, types.ModuleName, address, amount)
	if err != nil {
		return sdk.ErrInsufficientCoins(fmt.Sprintf("failed to deposits because insufficient coins saved(need %s)", amount.String()))
	}
	saving = saving.Sub(amount)
	k.SetSaving(ctx, product, saving)
	return nil
}

// GetSaving returns  the saving of product
func (k Keeper) GetSaving(ctx sdk.Context, product string) sdk.DecCoins {
	var saving sdk.DecCoins
	store := ctx.KVStore(k.storeKey)
	bytes := store.Get(types.GetSavingKey(product))
	if bytes == nil {
		return nil
	}

	if k.cdc.UnmarshalBinaryBare(bytes, &saving) != nil {
		ctx.Logger().Error("decoding of saving is failed", product)
		return nil
	}
	return saving
}

// SetSaving saves the saving of product to db
func (k Keeper) SetSaving(ctx sdk.Context, product string, amount sdk.DecCoins) {
	store := ctx.KVStore(k.storeKey)
	key := types.GetSavingKey(product)
	store.Set(key, k.cdc.MustMarshalBinaryBare(amount))
}

package rest

import (
	"encoding/hex"
	"encoding/json"
	"github.com/cosmos/cosmos-sdk/client/context"
	"github.com/cosmos/cosmos-sdk/types/rest"
	"github.com/cosmos/cosmos-sdk/x/auth/client/utils"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gorilla/mux"
	rpctypes "github.com/okex/okexchain/app/rpc/types"
	"net/http"
	"strings"
)

// RegisterRoutes - Central function to define routes that get registered by the main application
func RegisterRoutes(cliCtx context.CLIContext, r *mux.Router) {
	r.HandleFunc("/transaction/{hash}", QueryTxRequestHandlerFn(cliCtx)).Methods("GET")
}

func QueryTxRequestHandlerFn(cliCtx context.CLIContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		hashHexStr := vars["hash"]

		cliCtx, ok := rest.ParseQueryHeightOrReturnBadRequest(w, cliCtx, r)
		if !ok {
			return
		}
		var output interface{}
		output, err := utils.QueryTx(cliCtx, hashHexStr)
		if err != nil {
			output, err = getEthTransactionByHash(cliCtx, hashHexStr)
			if err != nil {
				if strings.Contains(err.Error(), hashHexStr) {
					rest.WriteErrorResponse(w, http.StatusNotFound, err.Error())
					return
				}
				rest.WriteErrorResponse(w, http.StatusInternalServerError, err.Error())
				return
			}
		}

		rest.PostProcessResponseBare(w, cliCtx, output)
	}
}


// GetTransactionByHash returns the transaction identified by hash.
func getEthTransactionByHash(cliCtx context.CLIContext, hashHex string) ([]byte, error) {
	hash, err := hex.DecodeString(hashHex)
	if err != nil {
		return nil, err
	}
	node, err := cliCtx.GetNode()
	if err != nil {
		return nil, err
	}
	tx, err := node.Tx(hash, false)

	// Can either cache or just leave this out if not necessary
	block, err := node.Block(&tx.Height)
	if err != nil {
		return nil, err
	}

	blockHash := common.BytesToHash(block.Block.Header.Hash())

	ethTx, err := rpctypes.RawTxToEthTx(cliCtx, tx.Tx)
	if err != nil {
		return nil, err
	}

	height := uint64(tx.Height)
	res, err := rpctypes.NewTransaction(ethTx, common.BytesToHash(tx.Tx.Hash()), blockHash, height, uint64(tx.Index))
	if err != nil {
		return nil, err
	}
	return json.Marshal(res)
}

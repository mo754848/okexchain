package client

import (
	"github.com/spf13/cobra"

	evmtypes "github.com/okex/okexchain/x/evm/types"
)

const (
	FlagPersonalAPI       = "personal-api"
	FlagGetLogsHeightSpan = "height-span"
)

func RegisterAppFlag(cmd *cobra.Command) {
	cmd.Flags().Bool(FlagPersonalAPI, true, "Enable the the personal_ prefixed set of APIs in the Web3 JSON-RPC spec")
	cmd.Flags().Bool(evmtypes.FlagEnableBloomFilter, true, "enable bloom filter for logs")
	cmd.Flags().Int64(FlagGetLogsHeightSpan, -1, "config the block height span for get logs")
}

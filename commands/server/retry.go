package server

import (
	"flag"
	"fmt"
	"io/ioutil"

	"github.com/tendermint/iavl"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/types"
)

const (
	flagUntilError = "error"
	flagMaxTries   = "max"
)

type retryArgs struct {
	dbPath     string
	blockPath  string
	debug      bool
	untilError bool
	maxTries   int
}

func parseRetryArgs(args []string) (retryArgs, error) {
	if len(args) < 2 {
		return retryArgs{}, fmt.Errorf("Usage: cmd retry <path to abci.db> <path to block.json> [-debug] [-error] [-max=N]")
	}
	res := retryArgs{
		dbPath:    args[0],
		blockPath: args[1],
	}
	getBlockFlags := flag.NewFlagSet("retry", flag.ExitOnError)
	getBlockFlags.BoolVar(&res.debug, flagDebug, false, "print out debug info")
	getBlockFlags.BoolVar(&res.untilError, flagUntilError, false, "retry multiple times until an error appears")
	getBlockFlags.IntVar(&res.maxTries, flagMaxTries, 10, "maximum number of times to retry if -error is passed")
	err := getBlockFlags.Parse(args[2:])
	return res, err
}

// RetryCmd takes the app state and the last block from the file system
// It verifies that they match, then rolls back one block and re-runs the given block
// It will output the new hash after running.
//
// If -error is passed, then it will try -max times until a different app hash results
func RetryCmd(logger log.Logger, home string, args []string) error {
	flags, err := parseRetryArgs(args)
	if err != nil {
		return err
	}

	fmt.Println("--> Loading Block")
	blockJSON, err := ioutil.ReadFile(flags.blockPath)
	if err != nil {
		return err
	}
	var block *types.Block
	err = cdc.UnmarshalJSON(blockJSON, &block)
	if err != nil {
		return err
	}

	fmt.Println("--> Loading Database")
	_, ver, err := readTree(flags.dbPath, 0)
	if err != nil {
		return fmt.Errorf("error reading abci data: %s", err)
	}

	if ver != block.Header.Height {
		return fmt.Errorf("Height mismatch - block=%d, abcistore=%d", block.Header.Height, ver)
	}

	return nil
}

func readTree(dir string, version int) (*iavl.MutableTree, int64, error) {
	db, err := openDb(dir)
	if err != nil {
		return nil, 0, err
	}
	tree := iavl.NewMutableTree(db, 10000) // cache size 10000
	ver, err := tree.LoadVersion(int64(version))
	if ver == 0 {
		return nil, 0, fmt.Errorf("iavl tree is empty")
	}
	return tree, ver, err
}

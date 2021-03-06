package main

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/docker/go-units"
	"github.com/ipfs/go-cid"
	"github.com/urfave/cli/v2"
	"golang.org/x/xerrors"

	"github.com/filecoin-project/go-fil-markets/storagemarket"
	"github.com/filecoin-project/specs-actors/actors/abi"

	"github.com/filecoin-project/lotus/build"
	"github.com/filecoin-project/lotus/chain/types"
	lcli "github.com/filecoin-project/lotus/cli"
)

var enableCmd = &cli.Command{
	Name:  "enable",
	Usage: "Configure the miner to consider storage deal proposals",
	Flags: []cli.Flag{},
	Action: func(cctx *cli.Context) error {
		api, closer, err := lcli.GetStorageMinerAPI(cctx)
		if err != nil {
			return err
		}
		defer closer()

		return api.DealsSetAcceptingStorageDeals(lcli.DaemonContext(cctx), true)
	},
}

var disableCmd = &cli.Command{
	Name:  "disable",
	Usage: "Configure the miner to reject all storage deal proposals",
	Flags: []cli.Flag{},
	Action: func(cctx *cli.Context) error {
		api, closer, err := lcli.GetStorageMinerAPI(cctx)
		if err != nil {
			return err
		}
		defer closer()

		return api.DealsSetAcceptingStorageDeals(lcli.DaemonContext(cctx), false)
	},
}

var setAskCmd = &cli.Command{
	Name:  "set-ask",
	Usage: "Configure the miner's ask",
	Flags: []cli.Flag{
		&cli.Uint64Flag{
			Name:     "price",
			Usage:    "Set the price of the ask (specified as FIL / GiB / Epoch) to `PRICE`",
			Required: true,
		},
		&cli.StringFlag{
			Name:        "duration",
			Usage:       "Set duration of ask (a quantity of time after which the ask expires) `DURATION`",
			DefaultText: "720h0m0s",
			Value:       "720h0m0s",
		},
		&cli.StringFlag{
			Name:        "min-piece-size",
			Usage:       "Set minimum piece size (w/bit-padding, in bytes) in ask to `SIZE`",
			DefaultText: "256B",
			Value:       "256B",
		},
		&cli.StringFlag{
			Name:        "max-piece-size",
			Usage:       "Set maximum piece size (w/bit-padding, in bytes) in ask to `SIZE`",
			DefaultText: "miner sector size",
		},
	},
	Action: func(cctx *cli.Context) error {
		ctx := lcli.DaemonContext(cctx)

		api, closer, err := lcli.GetStorageMinerAPI(cctx)
		if err != nil {
			return err
		}
		defer closer()

		pri := types.NewInt(cctx.Uint64("price"))

		dur, err := time.ParseDuration(cctx.String("duration"))
		if err != nil {
			return xerrors.Errorf("cannot parse duration: %w", err)
		}

		qty := dur.Seconds() / build.BlockDelay

		min, err := units.RAMInBytes(cctx.String("min-piece-size"))
		if err != nil {
			return xerrors.Errorf("cannot parse min-piece-size to quantity of bytes: %w", err)
		}

		if min < 256 {
			return xerrors.New("minimum piece size (w/bit-padding) is 256B")
		}

		max, err := units.RAMInBytes(cctx.String("max-piece-size"))
		if err != nil {
			return xerrors.Errorf("cannot parse max-piece-size to quantity of bytes: %w", err)
		}

		maddr, err := api.ActorAddress(ctx)
		if err != nil {
			return err
		}

		ssize, err := api.ActorSectorSize(ctx, maddr)
		if err != nil {
			return err
		}

		smax := int64(ssize)

		if max == 0 {
			max = smax
		}

		if max > smax {
			return xerrors.Errorf("max piece size (w/bit-padding) %s cannot exceed miner sector size %s", types.SizeStr(types.NewInt(uint64(max))), types.SizeStr(types.NewInt(uint64(smax))))
		}

		return api.MarketSetAsk(ctx, pri, abi.ChainEpoch(qty), abi.PaddedPieceSize(min), abi.PaddedPieceSize(max))
	},
}

var getAskCmd = &cli.Command{
	Name:  "get-ask",
	Usage: "Print the miner's ask",
	Flags: []cli.Flag{},
	Action: func(cctx *cli.Context) error {
		ctx := lcli.DaemonContext(cctx)

		fnapi, closer, err := lcli.GetFullNodeAPI(cctx)
		if err != nil {
			return err
		}
		defer closer()

		smapi, closer, err := lcli.GetStorageMinerAPI(cctx)
		if err != nil {
			return err
		}
		defer closer()

		sask, err := smapi.MarketGetAsk(ctx)
		if err != nil {
			return err
		}

		var ask *storagemarket.StorageAsk
		if sask != nil && sask.Ask != nil {
			ask = sask.Ask
		}

		w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
		fmt.Fprintf(w, "Price per GiB / Epoch\tMin. Piece Size (w/bit-padding)\tMax. Piece Size (w/bit-padding)\tExpiry (Epoch)\tExpiry (Appx. Rem. Time)\tSeq. No.\n")
		if ask == nil {
			fmt.Fprintf(w, "<miner does not have an ask>\n")

			return w.Flush()
		}

		head, err := fnapi.ChainHead(ctx)
		if err != nil {
			return err
		}

		dlt := ask.Expiry - head.Height()
		rem := "<expired>"
		if dlt > 0 {
			rem = (time.Second * time.Duration(dlt*build.BlockDelay)).String()
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%d\n", ask.Price, types.SizeStr(types.NewInt(uint64(ask.MinPieceSize))), types.SizeStr(types.NewInt(uint64(ask.MaxPieceSize))), ask.Expiry, rem, ask.SeqNo)

		return w.Flush()
	},
}

var dealsCmd = &cli.Command{
	Name:  "deals",
	Usage: "interact with your deals",
	Subcommands: []*cli.Command{
		dealsImportDataCmd,
		dealsListCmd,
		enableCmd,
		disableCmd,
		setAskCmd,
		getAskCmd,
	},
}

var dealsImportDataCmd = &cli.Command{
	Name:      "import-data",
	Usage:     "Manually import data for a deal",
	ArgsUsage: "<proposal CID> <file>",
	Action: func(cctx *cli.Context) error {
		api, closer, err := lcli.GetStorageMinerAPI(cctx)
		if err != nil {
			return err
		}
		defer closer()

		ctx := lcli.DaemonContext(cctx)

		if cctx.Args().Len() < 2 {
			return fmt.Errorf("must specify proposal CID and file path")
		}

		propCid, err := cid.Decode(cctx.Args().Get(0))
		if err != nil {
			return err
		}

		fpath := cctx.Args().Get(1)

		return api.DealsImportData(ctx, propCid, fpath)

	},
}

var dealsListCmd = &cli.Command{
	Name:  "list",
	Usage: "List all deals for this miner",
	Action: func(cctx *cli.Context) error {
		api, closer, err := lcli.GetStorageMinerAPI(cctx)
		if err != nil {
			return err
		}
		defer closer()

		ctx := lcli.DaemonContext(cctx)

		deals, err := api.MarketListIncompleteDeals(ctx)
		if err != nil {
			return err
		}

		data, err := json.MarshalIndent(deals, "", "  ")
		if err != nil {
			return err
		}

		fmt.Println(string(data))
		return nil
	},
}

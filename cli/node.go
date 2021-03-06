// This file is part of the go-kord library.
//
// Copyright (C) 2018 JAAK MUSIC LTD
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>.
//
// If you have any questions please contact yo@jaak.io

package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io/ioutil"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"unicode"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/cmd/utils"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/eth"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/node"
	"github.com/ethereum/go-ethereum/p2p/discover"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/swarm"
	swarmapi "github.com/ethereum/go-ethereum/swarm/api"
	"github.com/kord-network/go-kord/kord"
	"github.com/kord-network/go-kord/registry"
	"github.com/naoina/toml"
)

var testnetBootnodes = []string{
	"enode://21cd1409c28106062f79dbae8d9a69d4e1050c6f8a40ab63ec507c03970ed152c6f20708262f23a7334061fde7943b10ead6249bb88b2d7375d36f40ff471e82@35.176.243.138:30303",
}

func init() {
	registerCommand("node", RunNode, `
usage: kord node [--datadir <dir>] [--config <path>] [--dev] [--testnet] [--mine] [--root-dapp <uri>] [--cors-domain <domain>...]

Run a KORD node.

options:
	-d, --datadir <dir>         Node data directory
	-c, --config <path>         Path to the TOML config file
	--dev                       Run a dev node
	--testnet                   Connect to the testnet
	--mine                      Mine the Ethereum chain
	--root-dapp <uri>           Dapp to serve at root of KORD API
	--cors-domain <domain>...   The allowed CORS domains
`[1:])
}

func RunNode(ctx *Context) error {
	cfg := defaultConfig()

	if file := ctx.Args.String("--config"); file != "" {
		if err := loadConfig(file, &cfg); err != nil {
			return err
		}
	}

	switch {
	case ctx.Args.String("--datadir") != "":
		cfg.Node.DataDir = ctx.Args.String("--datadir")
	case ctx.Args.Bool("--dev"):
		tmpDir, err := ioutil.TempDir("", "kord-datadir")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmpDir)
		cfg.Node.DataDir = tmpDir
		cfg.Node.IPCPath = filepath.Join(os.TempDir(), cfg.Node.IPCPath)
	}

	if dapp := ctx.Args.String("--root-dapp"); dapp != "" {
		cfg.Kord.RootDapp = dapp
	}

	if _, ok := ctx.Args["--cors-domain"]; ok {
		domains := ctx.Args.List("--cors-domain")
		cfg.Swarm.Cors = strings.Join(domains, ",")
		cfg.Kord.CORSDomains = domains
	}

	if ctx.Args.Bool("--dev") && ctx.Args.Bool("--testnet") {
		return errors.New("--dev and --testnet cannot both be set")
	} else if ctx.Args.Bool("--dev") {
		// --dev mode can't use p2p networking.
		cfg.Node.P2P.MaxPeers = 0
		cfg.Node.P2P.ListenAddr = ":0"
		cfg.Node.P2P.NoDiscovery = true
		cfg.Node.P2P.DiscoveryV5 = false
	} else if ctx.Args.Bool("--testnet") {
		cfg.Eth.NetworkId = 1035
		cfg.Eth.Genesis = testnetGenesisBlock()

		if !ctx.Args.Bool("--mine") {
			cfg.Node.P2P.BootstrapNodes = make([]*discover.Node, 0, len(testnetBootnodes))
			for _, url := range testnetBootnodes {
				node, err := discover.ParseNode(url)
				if err != nil {
					return fmt.Errorf("invalid testnet bootnode: %s: %s", url, err)
				}
				cfg.Node.P2P.BootstrapNodes = append(cfg.Node.P2P.BootstrapNodes, node)
			}
		}
	}

	stack, err := node.New(&cfg.Node)
	if err != nil {
		return err
	}

	if ctx.Args.Bool("--dev") {
		if err := setupDevAccount(stack, &cfg); err != nil {
			return err
		}
	}

	utils.RegisterEthService(stack, &cfg.Eth)

	if err := registerSwarmService(stack, &cfg.Swarm); err != nil {
		return err
	}

	if err := registerKordService(stack, &cfg.Kord); err != nil {
		return err
	}

	// start the node
	if err := stack.Start(); err != nil {
		return err
	}

	// start mining if required or in dev mode
	if ctx.Args.Bool("--mine") || ctx.Args.Bool("--dev") {
		if err := startMining(stack, &cfg); err != nil {
			stack.Stop()
			return err
		}
	}

	if ctx.Args.Bool("--dev") {
		log.Info("deploying KORD registry")
		addr, err := registry.Deploy(stack.IPCEndpoint(), registry.DefaultConfig)
		if err != nil {
			log.Error("error deploying KORD registry", "err", err)
			stack.Stop()
			return err
		}
		log.Info("deployed KORD registry", "addr", addr)
	}

	// stop the node if the context is cancelled
	go func() {
		<-ctx.Done()
		stack.Stop()
	}()

	// wait for the node to exit
	stack.Wait()
	return nil
}

func registerSwarmService(stack *node.Node, cfg *swarmapi.Config) error {
	cfg.Path = stack.InstanceDir()

	// load the bzzaccount private key to initialise the config
	//
	// TODO: support getting the password from the user
	ks := stack.AccountManager().Backends(keystore.KeyStoreType)[0].(*keystore.KeyStore)
	account, err := ks.Find(accounts.Account{Address: common.HexToAddress(cfg.BzzAccount)})
	if err != nil {
		return err
	}
	keyjson, err := ioutil.ReadFile(account.URL.Path)
	if err != nil {
		return err
	}
	key, err := keystore.DecryptKey(keyjson, "")
	if err != nil {
		return err
	}
	cfg.Init(key.PrivateKey)

	return stack.Register(func(ctx *node.ServiceContext) (node.Service, error) {
		return swarm.NewSwarm(
			ctx,
			nil,
			nil,
			cfg,
			cfg.SwapEnabled,
			cfg.SyncEnabled,
			cfg.Cors,
		)
	})
}

func registerKordService(stack *node.Node, cfg *kord.Config) error {
	return stack.Register(func(ctx *node.ServiceContext) (node.Service, error) {
		return kord.New(ctx, stack, cfg)
	})
}

type config struct {
	Node  node.Config
	Eth   eth.Config
	Swarm swarmapi.Config
	Kord  kord.Config
}

func loadConfig(file string, cfg *config) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()

	err = tomlSettings.NewDecoder(bufio.NewReader(f)).Decode(cfg)
	// Add file name to errors that have a line number.
	if _, ok := err.(*toml.LineError); ok {
		err = errors.New(file + ", " + err.Error())
	}
	return err
}

func defaultConfig() config {
	swarmCfg := swarmapi.NewDefaultConfig()
	swarmCfg.Port = ""
	return config{
		Node:  defaultNodeConfig(),
		Eth:   eth.DefaultConfig,
		Swarm: *swarmCfg,
		Kord:  kord.DefaultConfig,
	}
}

func defaultNodeConfig() node.Config {
	cfg := node.DefaultConfig
	cfg.Name = "kord"
	cfg.Version = "0.0.1"
	cfg.HTTPModules = append(cfg.HTTPModules, "eth")
	cfg.WSModules = append(cfg.WSModules, "eth")
	cfg.IPCPath = "kord.ipc"
	return cfg
}

// These settings ensure that TOML keys use the same names as Go struct fields.
var tomlSettings = toml.Config{
	NormFieldName: func(rt reflect.Type, key string) string {
		return key
	},
	FieldToKey: func(rt reflect.Type, field string) string {
		return field
	},
	MissingField: func(rt reflect.Type, field string) error {
		link := ""
		if unicode.IsUpper(rune(rt.Name()[0])) && rt.PkgPath() != "main" {
			link = fmt.Sprintf(", see https://godoc.org/%s#%s for available fields", rt.PkgPath(), rt.Name())
		}
		return fmt.Errorf("field '%s' is not defined in %s%s", field, rt.String(), link)
	},
}

func setupDevAccount(stack *node.Node, cfg *config) error {
	// Import the developer account
	ks := stack.AccountManager().Backends(keystore.KeyStoreType)[0].(*keystore.KeyStore)
	developer, err := ks.ImportECDSA(registry.DevKey, "")
	if err != nil {
		return fmt.Errorf("error importing developer account: %s", err)
	}
	log.Info("Using developer account", "address", developer.Address)
	cfg.Swarm.BzzAccount = developer.Address.String()
	cfg.Eth.Genesis = core.DeveloperGenesisBlock(0, developer.Address)
	cfg.Eth.GasPrice = big.NewInt(1)
	return nil
}

func setLogVerbosity(v string) (int, error) {
	lvl, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("invalid --verbosity: %s", err)
	}
	handler := log.StreamHandler(os.Stderr, log.TerminalFormat(true))
	handler = log.LvlFilterHandler(log.Lvl(lvl), handler)
	log.Root().SetHandler(handler)
	return lvl, nil
}

func startMining(stack *node.Node, cfg *config) error {
	var ethereum *eth.Ethereum
	if err := stack.Service(&ethereum); err != nil {
		return fmt.Errorf("error getting Ethereum service: %s", err)
	}
	etherbase, err := ethereum.Etherbase()
	if err != nil {
		return fmt.Errorf("error getting Etherbase: %s", err)
	}
	// TODO: support keys with non-empty passphrase
	ks := stack.AccountManager().Backends(keystore.KeyStoreType)[0].(*keystore.KeyStore)
	if err := ks.Unlock(accounts.Account{Address: etherbase}, ""); err != nil {
		return fmt.Errorf("error unlocking Etherbase: %s", err)
	}
	ethereum.TxPool().SetGasPrice(cfg.Eth.GasPrice)
	if err := ethereum.StartMining(true); err != nil {
		return fmt.Errorf("error starting Ethereum mining: %s", err)
	}
	return nil
}

func testnetGenesisBlock() *core.Genesis {
	config := *params.AllCliqueProtocolChanges
	config.ChainId = big.NewInt(1035)
	return &core.Genesis{
		Config:     &config,
		Timestamp:  1518829335,
		ExtraData:  hexutil.MustDecode("0x0000000000000000000000000000000000000000000000000000000000000000b813999c1df85bb411dfd70b76635e834abc5fb80000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"),
		GasLimit:   4700000,
		Difficulty: big.NewInt(1),
		Alloc: map[common.Address]core.GenesisAccount{
			common.HexToAddress("0xb813999c1df85bb411dfd70b76635e834abc5fb8"): {Balance: new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(9))},
		},
	}
}

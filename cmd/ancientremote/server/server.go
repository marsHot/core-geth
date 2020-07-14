package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/cmd/utils"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/node"
	"github.com/ethereum/go-ethereum/rpc"
	"gopkg.in/urfave/cli.v1"
)

// DefaultHTTPTimeouts represents the default timeout values used if further
// configuration is not provided.
var DefaultHTTPTimeouts = rpc.HTTPTimeouts{
	ReadTimeout:  30 * time.Second,
	WriteTimeout: 30 * time.Second,
	IdleTimeout:  120 * time.Second,
}

type AncientHTTPServerConfig struct {
	vhosts       []string
	cors         []string
	httpEndpoint string
	httpTimeout  rpc.HTTPTimeouts
}

type AncientServerConfig struct {
	ipcPath    string
	httpConfig *AncientHTTPServerConfig
}

type AncientService interface {
	Start()
	Stop()
}

type AncientServer struct {
	cfg   *AncientServerConfig
	stop  func()
	start func()
}

func (server *AncientServer) Stop() {
	server.stop()
}

func (server *AncientServer) Start() {
	server.start()
}

func MakeServerConfig(c *cli.Context) AncientServerConfig {
	if args := c.Args(); len(args) > 0 {
		utils.Fatalf("invalid command: %q", args[0])
	}
	utils.CheckExclusive(c, IPCPathFlag, RPCPortFlag)
	ipcPath := c.GlobalString(IPCPathFlag.Name)

	vhosts := splitAndTrim(c.GlobalString(HTTPVirtualHostsFlag.Name))
	cors := splitAndTrim(c.GlobalString(HTTPCORSDomainFlag.Name))
	httpEndpoint := ""
	if c.GlobalBool(HTTPEnabledFlag.Name) {
		httpEndpoint = getHTTPEndpoint(c)
	}
	if err := setupLogFormat(c); err != nil {
		utils.Fatalf("Problem setting up logging %q", err)
	}
	// TODO add check for ipcPath nil
	return AncientServerConfig{
		ipcPath,
		&AncientHTTPServerConfig{
			vhosts,
			cors,
			httpEndpoint,
			DefaultHTTPTimeouts,
		},
	}
}

func checkImplementsRemoteFreezerAPI(rpcAPIs []rpc.API) {
	for _, api := range rpcAPIs {
		if _, ok := api.Service.(*rawdb.FreezerRemoteAPI); ok {
			return
		}
	}
	utils.Fatalf("Missing Ancient Store compliant API, please register a FreezerRemoteAPI service")
}

func newHTTPServer(cfg AncientServerConfig, rpcAPI []rpc.API, whitelist []string) AncientServer {
	var (
		httpServer *http.Server
		addr       net.Addr
		err        error
		extapiURL  string
	)
	httpConfig := cfg.httpConfig
	srv := rpc.NewServer()
	err = node.RegisterApisFromWhitelist(rpcAPI, whitelist, srv, false)
	if err != nil {
		utils.Fatalf("Could not register API: %w", err)
	}
	start := func() {
		log.Info("Starting HTTP based Freezer service")
		handler := node.NewHTTPHandlerStack(srv, httpConfig.cors, httpConfig.vhosts)
		httpServer, addr, err = node.StartHTTPEndpoint(httpConfig.httpEndpoint, rpc.DefaultHTTPTimeouts, handler)
		if err != nil {
			utils.Fatalf("Could not start RPC api: %v", err)
		}
		extapiURL = fmt.Sprintf("http://%v/", addr)
		log.Info("HTTP endpoint opened", "url", extapiURL)
	}

	stop := func() {
		// Don't bother imposing a timeout here.
		log.Info("Stopping HTTP based freezer service", "url", extapiURL)
		httpServer.Shutdown(context.Background())
		log.Info("HTTP endpoint closed", "url", extapiURL)
	}

	return AncientServer{cfg: &cfg, start: start, stop: stop}
}

// ipcEndpoint resolves an IPC endpoint based on a configured value
func ipcEndpoint(ipcPath string) string {
	// On windows we can only use plain top-level pipes
	if runtime.GOOS == "windows" {
		if strings.HasPrefix(ipcPath, `\\.\pipe\`) {
			return ipcPath
		}
		return `\\.\pipe\` + ipcPath
	}
	return ipcPath
}

func newIPCServer(cfg *AncientServerConfig, rpcAPI []rpc.API) AncientServer {
	ipcPath := cfg.ipcPath
	ipcapiURL := ipcEndpoint(ipcPath)
	var (
		listener net.Listener
		err      error
	)
	start := func() {
		listener, _, err = rpc.StartIPCEndpoint(ipcapiURL, rpcAPI)
		if err != nil {
			utils.Fatalf("Could not start IPC api: %v", err)
		}
		log.Info("IPC endpoint opened", "url", ipcapiURL)
	}
	stop := func() {
		listener.Close()
		log.Info("IPC endpoint closed", "url", ipcapiURL)
	}
	return AncientServer{cfg: cfg, start: start, stop: stop}
}

// NewServer constructs an AncientServer from the AncientServerConfig
func NewServer(cfg AncientServerConfig, rpcAPI []rpc.API, whitelist []string) AncientServer {

	checkImplementsRemoteFreezerAPI(rpcAPI)

	if cfg.httpConfig.httpEndpoint != "" {
		return newHTTPServer(cfg, rpcAPI, whitelist)
	}
	return newIPCServer(&cfg, rpcAPI)
}
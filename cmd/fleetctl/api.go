package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"runtime"

	"github.com/fleetdm/fleet/v4/pkg/fleethttp"
	"github.com/fleetdm/fleet/v4/server/service"
	"github.com/kolide/kit/version"
	"github.com/urfave/cli/v2"
)

func unauthenticatedClientFromCLI(c *cli.Context) (*service.Client, error) {
	cc, err := clientConfigFromCLI(c)
	if err != nil {
		return nil, err
	}

	return unauthenticatedClientFromConfig(cc, getDebug(c), c.App.Writer)
}

func clientFromCLI(c *cli.Context) (*service.Client, error) {
	fleet, err := unauthenticatedClientFromCLI(c)
	if err != nil {
		return nil, err
	}

	configPath, context := c.String("config"), c.String("context")

	// if a config file is explicitly provided, do not set an invalid arbitrary token
	if !c.IsSet("config") && flag.Lookup("test.v") != nil {
		fleet.SetToken("AAAA")
		return fleet, nil
	}

	// Add authentication token
	t, err := getConfigValue(configPath, context, "token")
	if err != nil {
		return nil, fmt.Errorf("error getting token from the config: %w", err)
	}

	token, ok := t.(string)
	if !ok {
		fmt.Fprintln(os.Stderr, "Token invalid. Please log in with: fleetctl login")
		return nil, fmt.Errorf("token config value expected type %T, got %T: %+v", "", t, t)
	}
	if token == "" {
		fmt.Fprintln(os.Stderr, "Token missing. Please log in with: fleetctl login")
		return nil, errors.New("token config value missing")
	}
	fleet.SetToken(token)

	// Check if version matches fleet server. Also ensures that the token is valid.
	clientInfo := version.Version()

	serverInfo, err := fleet.Version()
	if err != nil {
		if errors.Is(err, service.ErrUnauthenticated) {
			fmt.Fprintln(os.Stderr, "Token invalid or session expired. Please log in with: fleetctl login")
		}
		return nil, err
	}

	if clientInfo.Version != serverInfo.Version {
		fmt.Fprintf(
			os.Stderr,
			"Warning: Version mismatch.\nClient Version:   %s\nServer Version:  %s\n",
			clientInfo.Version, serverInfo.Version,
		)
		// This is just a warning, continue ...
	}

	return fleet, nil
}

func unauthenticatedClientFromConfig(cc Context, debug bool, w io.Writer) (*service.Client, error) {
	options := []service.ClientOption{service.SetClientWriter(w)}

	if flag.Lookup("test.v") != nil {
		return service.NewClient(
			os.Getenv("FLEET_SERVER_ADDRESS"), true, "", "", options...)
	}

	if cc.Address == "" {
		return nil, errors.New("set the Fleet API address with: fleetctl config set --address https://localhost:8080")
	}

	if runtime.GOOS == "windows" && cc.RootCA == "" && !cc.TLSSkipVerify {
		return nil, errors.New("Windows clients must configure rootca (secure) or tls-skip-verify (insecure)")
	}

	if debug {
		options = append(options, service.EnableClientDebug())
	}

	fleet, err := service.NewClient(
		cc.Address,
		cc.TLSSkipVerify,
		cc.RootCA,
		cc.URLPrefix,
		options...,
	)
	if err != nil {
		return nil, fmt.Errorf("error creating Fleet API client handler: %w", err)
	}

	return fleet, nil
}

// returns an HTTP client and the parsed URL for the configured server's
// address. The reason why this exists instead of using
// unauthenticatedClientFromConfig is because this doesn't apply the same rules
// around TLS config - in particular, it only sets a root CA if one is
// explicitly configured.
func rawHTTPClientFromConfig(cc Context) (*http.Client, *url.URL, error) {
	if flag.Lookup("test.v") != nil {
		cc.Address = os.Getenv("FLEET_SERVER_ADDRESS")
	}
	baseURL, err := url.Parse(cc.Address)
	if err != nil {
		return nil, nil, fmt.Errorf("parse address: %w", err)
	}

	var rootCA *x509.CertPool
	if cc.RootCA != "" {
		rootCA = x509.NewCertPool()
		// read in the root cert file specified in the context
		certs, err := ioutil.ReadFile(cc.RootCA)
		if err != nil {
			return nil, nil, fmt.Errorf("reading root CA: %w", err)
		}

		// add certs to pool
		if ok := rootCA.AppendCertsFromPEM(certs); !ok {
			return nil, nil, errors.New("failed to add certificates to root CA pool")
		}
	}

	cli := fleethttp.NewClient(fleethttp.WithTLSClientConfig(&tls.Config{
		InsecureSkipVerify: cc.TLSSkipVerify,
		RootCAs:            rootCA,
	}))
	return cli, baseURL, nil
}

func clientConfigFromCLI(c *cli.Context) (Context, error) {
	if flag.Lookup("test.v") != nil {
		return Context{
			Address:       os.Getenv("FLEET_SERVER_ADDRESS"),
			TLSSkipVerify: true,
		}, nil
	}

	var zeroCtx Context

	if err := makeConfigIfNotExists(c.String("config")); err != nil {
		return zeroCtx, fmt.Errorf("error verifying that config exists at %s: %w", c.String("config"), err)
	}

	config, err := readConfig(c.String("config"))
	if err != nil {
		return zeroCtx, err
	}

	cc, ok := config.Contexts[c.String("context")]
	if !ok {
		return zeroCtx, fmt.Errorf("context %q is not found", c.String("context"))
	}
	return cc, nil
}

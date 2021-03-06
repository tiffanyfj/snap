/*
http://www.apache.org/licenses/LICENSE-2.0.txt


Copyright 2015 Intel Corporation

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/codegangsta/cli"

	"github.com/intelsdi-x/snap/control"
	"github.com/intelsdi-x/snap/core"
	"github.com/intelsdi-x/snap/core/serror"
	"github.com/intelsdi-x/snap/mgmt/rest"
	"github.com/intelsdi-x/snap/mgmt/tribe"
	"github.com/intelsdi-x/snap/mgmt/tribe/agreement"
	"github.com/intelsdi-x/snap/scheduler"
)

var (
	flAPIDisabled = cli.BoolFlag{
		Name:  "disable-api, d",
		Usage: "Disable the agent REST API",
	}
	flAPIPort = cli.IntFlag{
		Name:  "api-port,  p",
		Usage: "API port (Default: 8181)",
		Value: 8181,
	}
	flMaxProcs = cli.IntFlag{
		Name:   "max-procs, c",
		Usage:  "Set max cores to use for snap Agent. Default is 1 core.",
		Value:  1,
		EnvVar: "GOMAXPROCS",
	}
	flNumberOfPLs = cli.IntFlag{
		Name:   "max-running-plugins, m",
		Usage:  "The maximum number of instances of a loaded plugin to run",
		Value:  3,
		EnvVar: "SNAP_MAX_PLUGINS",
	}
	// plugin
	flLogPath = cli.StringFlag{
		Name:   "log-path, o",
		Usage:  "Path for logs. Empty path logs to stdout.",
		EnvVar: "SNAP_LOG_PATH",
	}
	flLogLevel = cli.IntFlag{
		Name:   "log-level, l",
		Usage:  "1-5 (Debug, Info, Warning, Error, Fatal)",
		EnvVar: "SNAP_LOG_LEVEL",
		Value:  3,
	}
	flPluginVersion = cli.StringFlag{
		Name:   "auto-discover, a",
		Usage:  "Auto discover paths separated by colons.",
		EnvVar: "SNAP_AUTOLOAD_PATH",
	}
	flPluginTrust = cli.IntFlag{
		Name:   "plugin-trust, t",
		Usage:  "0-2 (Disabled, Enabled, Warning)",
		EnvVar: "SNAP_TRUST_LEVEL",
		Value:  1,
	}
	flkeyringPaths = cli.StringFlag{
		Name:   "keyring-files, k",
		Usage:  "Keyring files for signing verification separated by colons",
		EnvVar: "SNAP_KEYRING_FILES",
	}
	flCache = cli.StringFlag{
		Name:   "cache-expiration",
		Usage:  "The time limit for which a metric cache entry is valid",
		EnvVar: "SNAP_CACHE_EXPIRATION",
		Value:  "500ms",
	}
	flConfig = cli.StringFlag{
		Name:  "config",
		Usage: "A path to a config file",
	}
	flRestHttps = cli.BoolFlag{
		Name:  "rest-https",
		Usage: "start snap's API as https",
	}
	flRestCert = cli.StringFlag{
		Name:  "rest-cert",
		Usage: "A path to a certificate to use for HTTPS deployment of snap's REST API",
	}
	flRestKey = cli.StringFlag{
		Name:  "rest-key",
		Usage: "A path to a key file to use for HTTPS deployment of snap's REST API",
	}

	gitversion string
)

const (
	defaultQueueSize uint = 25
	defaultPoolSize  uint = 4
)

type coreModule interface {
	Start() error
	Stop()
	Name() string
}

type managesTribe interface {
	GetAgreement(name string) (*agreement.Agreement, serror.SnapError)
	GetAgreements() map[string]*agreement.Agreement
	AddAgreement(name string) serror.SnapError
	RemoveAgreement(name string) serror.SnapError
	JoinAgreement(agreementName, memberName string) serror.SnapError
	LeaveAgreement(agreementName, memberName string) serror.SnapError
	GetMembers() []string
	GetMember(name string) *agreement.Member
}

var coreModules []coreModule

func main() {
	// Add a check to see if gitversion is blank from the build process
	if gitversion == "" {
		gitversion = "unknown"
	}

	app := cli.NewApp()
	app.Name = "snapd"
	app.Version = gitversion
	app.Usage = "A powerful telemetry framework"
	app.Flags = []cli.Flag{
		flAPIDisabled,
		flAPIPort,
		flLogLevel,
		flLogPath,
		flMaxProcs,
		flPluginVersion,
		flNumberOfPLs,
		flCache,
		flPluginTrust,
		flkeyringPaths,
		flRestCert,
		flConfig,
		flRestHttps,
		flRestKey,
	}
	app.Flags = append(app.Flags, tribe.Flags...)

	app.Action = action
	app.Run(os.Args)
}

func action(ctx *cli.Context) {
	// If logPath is set, we verify the logPath and set it so that all logging
	// goes to the log file instead of stdout.
	logPath := ctx.String("log-path")
	if logPath != "" {
		f, err := os.Stat(logPath)
		if err != nil {
			log.Fatal(err)
		}
		if !f.IsDir() {
			log.Fatal("log path provided must be a directory")
		}

		file, err := os.OpenFile(fmt.Sprintf("%s/snap.log", logPath), os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
		if err != nil {
			log.Fatal(err)
		}
		defer file.Close()
		log.SetOutput(file)
	}

	var l = map[int]string{
		1: "debug",
		2: "info",
		3: "warning",
		4: "error",
		5: "fatal",
	}

	var t = map[int]string{
		0: "disabled",
		1: "enabled",
		2: "warning",
	}

	logLevel := ctx.Int("log-level")
	maxProcs := ctx.Int("max-procs")
	disableAPI := ctx.Bool("disable-api")
	apiPort := ctx.Int("api-port")
	autodiscoverPath := ctx.String("auto-discover")
	maxRunning := ctx.Int("max-running-plugins")
	pluginTrust := ctx.Int("plugin-trust")
	keyringPaths := ctx.String("keyring-files")
	cachestr := ctx.String("cache-expiration")
	isTribeEnabled := ctx.Bool("tribe")
	tribeSeed := ctx.String("tribe-seed")
	tribeNodeName := ctx.String("tribe-node-name")
	tribeAddr := ctx.String("tribe-addr")
	tribePort := ctx.Int("tribe-port")
	cache, err := time.ParseDuration(cachestr)
	if err != nil {
		log.Fatal(fmt.Sprintf("invalid cache-expiration format: %s", cachestr))
	}
	config := ctx.String("config")
	restHttps := ctx.Bool("rest-https")
	restKey := ctx.String("rest-key")
	restCert := ctx.String("rest-cert")

	log.Info("Starting snapd (version: ", gitversion, ")")

	// Set Max Processors for snapd.
	setMaxProcs(maxProcs)

	// Validate log level and trust level settings for snapd
	validateLevelSettings(logLevel, pluginTrust)

	controlOpts := []control.PluginControlOpt{
		control.MaxRunningPlugins(maxRunning),
		control.CacheExpiration(cache),
	}

	if config != "" {
		b, err := ioutil.ReadFile(config)
		if err != nil {
			log.WithFields(log.Fields{
				"block":   "main",
				"_module": "snapd",
				"error":   err.Error(),
				"path":    config,
			}).Fatal("unable to read config")
		}
		cfg := control.NewConfig()
		err = json.Unmarshal(b, &cfg)
		if err != nil {
			log.WithFields(log.Fields{
				"block":   "main",
				"_module": "snapd",
				"error":   err.Error(),
				"path":    config,
			}).Fatal("invalid config")
		}
		controlOpts = append(controlOpts, control.OptSetConfig(cfg))
	}

	c := control.New(
		controlOpts...,
	)

	coreModules = []coreModule{}

	coreModules = append(coreModules, c)
	s := scheduler.New(
		scheduler.CollectQSizeOption(defaultQueueSize),
		scheduler.CollectWkrSizeOption(defaultPoolSize),
		scheduler.PublishQSizeOption(defaultQueueSize),
		scheduler.PublishWkrSizeOption(defaultPoolSize),
		scheduler.ProcessQSizeOption(defaultQueueSize),
		scheduler.ProcessWkrSizeOption(defaultPoolSize),
	)
	s.SetMetricManager(c)
	coreModules = append(coreModules, s)

	var tr managesTribe
	if isTribeEnabled {
		log.Info("Tribe is enabled")
		tc := tribe.DefaultConfig(tribeNodeName, tribeAddr, tribePort, tribeSeed, apiPort)
		t, err := tribe.New(tc)
		if err != nil {
			printErrorAndExit(t.Name(), err)
		}
		c.RegisterEventHandler("tribe", t)
		t.SetPluginCatalog(c)
		s.RegisterEventHandler("tribe", t)
		t.SetTaskManager(s)
		coreModules = append(coreModules, t)
		tr = t
	}

	// Set interrupt handling so we can die gracefully.
	startInterruptHandling(coreModules...)

	//  Start our modules
	var started []coreModule
	for _, m := range coreModules {
		if err := startModule(m); err != nil {
			for _, m := range started {
				m.Stop()
			}
			printErrorAndExit(m.Name(), err)
		}
		started = append(started, m)
	}

	//Plugin Trust
	c.SetPluginTrustLevel(pluginTrust)
	log.Info("setting plugin trust level to: ", t[pluginTrust])
	//Keyring checking for trust levels 1 and 2

	if pluginTrust > 0 {
		keyrings := filepath.SplitList(keyringPaths)
		if len(keyrings) == 0 {
			log.WithFields(
				log.Fields{
					"block":   "main",
					"_module": "snapd",
				}).Fatal("need keyring file when trust is on (--keyring-file or -k)")
		}
		for _, k := range keyrings {
			keyringPath, err := filepath.Abs(k)
			if err != nil {
				log.WithFields(
					log.Fields{
						"block":       "main",
						"_module":     "snapd",
						"error":       err.Error(),
						"keyringPath": keyringPath,
					}).Fatal("Unable to determine absolute path to keyring file")
			}
			f, err := os.Stat(keyringPath)
			if err != nil {
				log.WithFields(
					log.Fields{
						"block":       "main",
						"_module":     "snapd",
						"error":       err.Error(),
						"keyringPath": keyringPath,
					}).Fatal("bad keyring file")
			}
			if f.IsDir() {
				log.Info("Adding keyrings from: ", keyringPath)
				files, err := ioutil.ReadDir(keyringPath)
				if err != nil {
					log.WithFields(
						log.Fields{
							"_block":      "main",
							"_module":     "snapd",
							"error":       err.Error(),
							"keyringPath": keyringPath,
						}).Fatal(err)
				}
				if len(files) == 0 {
					log.Fatal(fmt.Sprintf("given keyring path [%s] is an empty directory!", keyringPath))
				}
				for _, keyringFile := range files {
					if keyringFile.IsDir() {
						continue
					}
					if strings.HasSuffix(keyringFile.Name(), ".gpg") || (strings.HasSuffix(keyringFile.Name(), ".pub")) || (strings.HasSuffix(keyringFile.Name(), ".pubring")) {
						f, err := os.Open(keyringPath)
						if err != nil {
							log.WithFields(
								log.Fields{
									"block":       "main",
									"_module":     "snapd",
									"error":       err.Error(),
									"keyringPath": keyringPath,
								}).Warning("unable to open keyring file. not adding to keyring path")
							continue
						}
						f.Close()
						log.Info("adding keyring file: ", keyringPath+"/"+keyringFile.Name())
						c.SetKeyringFile(keyringPath + "/" + keyringFile.Name())
					}
				}
			} else {
				f, err := os.Open(keyringPath)
				if err != nil {
					log.WithFields(
						log.Fields{
							"block":       "main",
							"_module":     "snapd",
							"error":       err.Error(),
							"keyringPath": keyringPath,
						}).Fatal("unable to open keyring file.")
				}
				f.Close()
				log.Info("adding keyring file ", keyringPath)
				c.SetKeyringFile(keyringPath)
			}
		}
	}

	//Autodiscover
	if autodiscoverPath != "" {
		log.Info("auto discover path is enabled")
		paths := filepath.SplitList(autodiscoverPath)
		c.SetAutodiscoverPaths(paths)
		for _, p := range paths {
			fullPath, err := filepath.Abs(p)
			if err != nil {
				log.WithFields(
					log.Fields{
						"_block":           "main",
						"_module":          "snapd",
						"autodiscoverpath": p,
					}).Fatal(err)
			}
			log.Info("autoloading plugins from: ", fullPath)
			files, err := ioutil.ReadDir(fullPath)
			if err != nil {
				log.WithFields(
					log.Fields{
						"_block":           "main",
						"_module":          "snapd",
						"autodiscoverpath": fullPath,
					}).Fatal(err)
			}
			for _, file := range files {
				if file.IsDir() {
					continue
				}
				if strings.HasSuffix(file.Name(), ".aci") || !(strings.HasSuffix(file.Name(), ".asc")) {
					rp, err := core.NewRequestedPlugin(path.Join(fullPath, file.Name()))
					if err != nil {
						log.WithFields(log.Fields{
							"_block":           "main",
							"_module":          "snapd",
							"autodiscoverpath": fullPath,
							"plugin":           file,
						}).Error(err)
					}
					signatureFile := file.Name() + ".asc"
					if _, err := os.Stat(path.Join(fullPath, signatureFile)); err == nil {
						err = rp.ReadSignatureFile(path.Join(fullPath, signatureFile))
						if err != nil {
							log.WithFields(log.Fields{
								"_block":           "main",
								"_module":          "snapd",
								"autodiscoverpath": fullPath,
								"plugin":           file.Name() + ".asc",
							}).Error(err)
						}
					}
					pl, err := c.Load(rp)
					if err != nil {
						log.WithFields(log.Fields{
							"_block":           "main",
							"_module":          "snapd",
							"autodiscoverpath": fullPath,
							"plugin":           file,
						}).Error(err)
					} else {
						log.WithFields(log.Fields{
							"_block":           "main",
							"_module":          "snapd",
							"autodiscoverpath": fullPath,
							"plugin":           file,
							"plugin-name":      pl.Name(),
							"plugin-version":   pl.Version(),
							"plugin-type":      pl.TypeName(),
						}).Info("Loading plugin")
					}
				}
			}
		}
	} else {
		log.Info("auto discover path is disabled")
	}

	//API
	if !disableAPI {
		r, err := rest.New(restHttps, restCert, restKey)
		if err != nil {
			log.Fatal(err)
			return
		}
		r.BindMetricManager(c)
		r.BindConfigManager(c.Config)
		r.BindTaskManager(s)
		if tr != nil {
			r.BindTribeManager(tr)
		}
		r.Start(fmt.Sprintf(":%d", apiPort))
		go monitorErrors(r.Err())
		log.Info("Rest API is enabled")
	} else {
		log.Info("Rest API is disabled")
	}

	log.WithFields(
		log.Fields{
			"block":   "main",
			"_module": "snapd",
		}).Info("snapd started")

	// Switch log level to user defined
	log.Info("setting log level to: ", l[logLevel])
	log.SetLevel(getLevel(logLevel))

	select {} //run forever and ever
}

func monitorErrors(ch <-chan error) {
	// Block on the error channel. Will return exit status 1 for an error or just return if the channel closes.
	_, ok := <-ch
	if !ok {
		return
	}
	os.Exit(1)
}

// setMaxProcs configures runtime.GOMAXPROCS for snapd. GOMAXPROCS can be set by using
// the env variable GOMAXPROCS and snapd will honor this setting. A user can override the env
// variable by setting max-procs flag on the command line. Snapd will be limited to the max CPUs
// on the system even if the env variable or the command line setting is set above the max CPUs.
// The default value if the env variable or the command line option is not set is 1.
func setMaxProcs(maxProcs int) {
	var _maxProcs int
	numProcs := runtime.NumCPU()
	if maxProcs <= 0 {
		// We prefer sane values for GOMAXPROCS
		log.WithFields(
			log.Fields{
				"_block":   "main",
				"_module":  "snapd",
				"maxprocs": maxProcs,
			}).Error("Trying to set GOMAXPROCS to an invalid value")
		_maxProcs = 1
		log.WithFields(
			log.Fields{
				"_block":   "main",
				"_module":  "snapd",
				"maxprocs": _maxProcs,
			}).Warning("Setting GOMAXPROCS to 1")
		_maxProcs = 1
	} else if maxProcs <= numProcs {
		_maxProcs = maxProcs
	} else {
		log.WithFields(
			log.Fields{
				"_block":   "main",
				"_module":  "snapd",
				"maxprocs": maxProcs,
			}).Error("Trying to set GOMAXPROCS larger than number of CPUs available on system")
		_maxProcs = numProcs
		log.WithFields(
			log.Fields{
				"_block":   "main",
				"_module":  "snapd",
				"maxprocs": _maxProcs,
			}).Warning("Setting GOMAXPROCS to number of CPUs on host")
	}

	log.Info("setting GOMAXPROCS to: ", _maxProcs, " core(s)")
	runtime.GOMAXPROCS(_maxProcs)
	//Verify setting worked
	actualNumProcs := runtime.GOMAXPROCS(0)
	if actualNumProcs != _maxProcs {
		log.WithFields(
			log.Fields{
				"block":          "main",
				"_module":        "snapd",
				"given maxprocs": _maxProcs,
				"real maxprocs":  actualNumProcs,
			}).Warning("not using given maxprocs")
	}
}

func startModule(m coreModule) error {
	err := m.Start()
	if err == nil {
		log.WithFields(
			log.Fields{
				"block":       "main",
				"_module":     "snapd",
				"snap-module": m.Name(),
			}).Info("module started")
	}
	return err
}

func printErrorAndExit(name string, err error) {
	log.WithFields(
		log.Fields{
			"block":       "main",
			"_module":     "snapd",
			"error":       err.Error(),
			"snap-module": name,
		}).Fatal("error starting module")
}

func startInterruptHandling(modules ...coreModule) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, os.Kill, syscall.SIGTERM)

	//Let's block until someone tells us to quit
	go func() {
		sig := <-c
		log.WithFields(
			log.Fields{
				"block":   "main",
				"_module": "snapd",
			}).Info("shutting down modules")

		for _, m := range modules {
			log.WithFields(
				log.Fields{
					"block":       "main",
					"_module":     "snapd",
					"snap-module": m.Name(),
				}).Info("stopping module")
			m.Stop()
		}
		log.WithFields(
			log.Fields{
				"block":   "main",
				"_module": "snapd",
				"signal":  sig.String(),
			}).Info("exiting on signal")
		os.Exit(0)
	}()
}

func getLevel(i int) log.Level {
	switch i {
	case 1:
		return log.DebugLevel
	case 2:
		return log.InfoLevel
	case 3:
		return log.WarnLevel
	case 4:
		return log.ErrorLevel
	case 5:
		return log.FatalLevel
	default:
		panic("bad level")
	}
}

func validateLevelSettings(logLevel, pluginTrust int) {
	if logLevel < 1 || logLevel > 5 {
		log.WithFields(
			log.Fields{
				"block":   "main",
				"_module": "snapd",
				"level":   logLevel,
			}).Fatal("log level was invalid (needs: 1-5)")
	}
	if pluginTrust < 0 || pluginTrust > 2 {
		log.WithFields(
			log.Fields{
				"block":   "main",
				"_module": "snapd",
				"level":   pluginTrust,
			}).Fatal("Plugin trust was invalid (needs: 0-2)")
	}
}

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
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/codegangsta/cli"
	"github.com/intelsdi-x/snap/mgmt/rest/client"
	"golang.org/x/crypto/ssh/terminal"
)

var (
	gitversion string
	pClient    *client.Client
	timeFormat = time.RFC1123
)

func main() {
	app := cli.NewApp()
	app.Name = "snapctl"
	app.Version = gitversion
	app.Usage = "A powerful telemetry framework"
	app.Flags = []cli.Flag{flURL, flSecure, flAPIVer, flPassword, flPasswordPath}
	app.Commands = commands
	sort.Sort(ByCommand(app.Commands))
	app.Run(os.Args)
}

func init() {
	f1 := flag.NewFlagSet("f1", flag.ContinueOnError)
	prtURL := f1.String("url", flURL.Value, flURL.Usage)
	prtU := f1.String("u", flURL.Value, flURL.Usage)
	prtAv := f1.String("api-version", flAPIVer.Value, flAPIVer.Usage)
	prtA := f1.String("a", flAPIVer.Value, flAPIVer.Usage)
	prti := f1.Bool("insecure", false, flSecure.Usage)
	prtPath := f1.String("Password", "", flPasswordPath.Usage)
	prtP := f1.String("P", "", flPasswordPath.Usage)

	url := flURL.Value
	ver := flAPIVer.Value
	secure := false
	passPrompt := false
	passPathProvided := false
	passPath := ""
	help := false

	if len(os.Args) == 1 {
		help = true
	}
	// get password path from env variable but overwrite
	// if value passed as flag for either a path to a password
	// file or a for a prompt
	tmpPath := os.Getenv(flPasswordPath.EnvVar)
	if tmpPath != "" {
		passPathProvided = true
		passPath = tmpPath
	}

	for idx, a := range os.Args {
		switch a {
		case "--url":
			if len(os.Args) >= idx+2 {
				if err := f1.Parse(os.Args[idx : idx+2]); err == nil {
					url = *prtURL
				}
			}
		case "-u":
			if len(os.Args) >= idx+2 {
				if err := f1.Parse(os.Args[idx : idx+2]); err == nil {
					url = *prtU
				}
			}
		case "--api-version":
			if len(os.Args) >= idx+2 {
				if err := f1.Parse(os.Args[idx : idx+2]); err == nil {
					ver = *prtAv
				}
			}
		case "-a":
			if len(os.Args) >= idx+2 {
				if err := f1.Parse(os.Args[idx : idx+2]); err == nil {
					ver = *prtA
				}
			}
		case "--insecure":
			if err := f1.Parse([]string{os.Args[idx]}); err == nil {
				secure = *prti
			}
		case "--password", "-p":
			passPrompt = true
		case "--Password":
			passPathProvided = true
			if len(os.Args) >= idx+2 {
				if err := f1.Parse(os.Args[idx : idx+2]); err == nil {
					passPath = *prtPath
				}
			}
		case "-P":
			passPathProvided = true
			if len(os.Args) >= idx+2 {
				if err := f1.Parse(os.Args[idx : idx+2]); err == nil {
					passPath = *prtP
				}
			}
		case "-h", "--help":
			help = true
		}
	}
	// Static username since username is not supported
	username := "snap"
	password := ""
	pClient = client.New(url, ver, secure)

	// If we have a pass path and not a pass prompt use the path
	if passPathProvided && !passPrompt {
		val, err := ioutil.ReadFile(passPath)
		if err == nil {
			password = string(val)
		}
	}
	// if pass prompt  -- use the prompt over all other options
	if passPrompt {
		// Prompt for password
		fmt.Print("Password:")
		pass, err := terminal.ReadPassword(0)
		if err != nil {
			password = ""
		} else {
			password = string(pass)
		}
		// Go to next line after password prompt
		fmt.Println()
	}
	pClient.Username = username
	pClient.Password = strings.TrimSpace(password)
	// Send tribe agreement requests only if needed by help
	if help {
		resp := pClient.ListAgreements()
		if resp.Err == nil {
			commands = append(commands, tribeCommands...)
		}
	}

}

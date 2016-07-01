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
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/codegangsta/cli"
	"github.com/intelsdi-x/snap/mgmt/rest/rbody"
)

func listMetrics(ctx *cli.Context) {
	ns := ctx.String("metric-namespace")
	ver := ctx.Int("metric-version")
	verbose := ctx.Bool("verbose")
	if ns != "" {
		//if the user doesn't provide '/*' we fix it
		if ns[len(ns)-2:] != "/*" {
			if ns[len(ns)-1:] == "/" {
				ns = ns + "*"
			} else {
				ns = ns + "/*"
			}
		}
	} else {
		ns = "/*"
	}
	mts := pClient.FetchMetrics(ns, ver)
	if mts.Err != nil {
		fmt.Printf("Error getting metrics: %v\n", mts.Err)
		os.Exit(1)
	}

	/*
		NAMESPACE               VERSION
		/intel/mock/foo         1,2
		/intel/mock/bar         1
	*/
	w := tabwriter.NewWriter(os.Stdout, 0, 8, 1, '\t', 0)

	if verbose {

		//	NAMESPACE                VERSION         UNIT          DESCRIPTION
		//	/intel/mock/foo          1
		//      /intel/mock/foo          2               mock unit     mock description
		//      /intel/mock/[host]/baz   2               mock unit     mock description

		printFields(w, false, 0, "NAMESPACE", "VERSION", "UNIT", "DESCRIPTION")
		for _, mt := range mts.Catalog {
			namespace := getNamespace(mt)
			printFields(w, false, 0, namespace, mt.Version, mt.Unit, mt.Description)
		}
		w.Flush()
		return
	}
	metsByVer := make(map[string][]string)
	for _, mt := range mts.Catalog {
		metsByVer[mt.Namespace] = append(metsByVer[mt.Namespace], strconv.Itoa(mt.Version))
	}
	//make list in alphabetical order
	var key []string
	for k := range metsByVer {
		key = append(key, k)
	}
	sort.Strings(key)

	printFields(w, false, 0, "NAMESPACE", "VERSIONS")
	for _, ns := range key {
		printFields(w, false, 0, ns, strings.Join(metsByVer[ns], ","))
	}
	w.Flush()
	return
}

func getMetric(ctx *cli.Context) {
	var metrics []*rbody.Metric
	if !ctx.IsSet("metric-namespace") {
		fmt.Println("namespace is required")
		fmt.Println("")
		cli.ShowCommandHelp(ctx, ctx.Command.Name)
		return
	}
	ns := ctx.String("metric-namespace")
	ver := ctx.Int("metric-version")
	if string(ns[len(ns)-1]) == "*" {
		mts := pClient.FetchMetrics("/intel/mock", ver)
		if mts.Err != nil {
			fmt.Println(mts.Err)
			return
		}
		metrics = append(metrics, mts.Catalog...)
	}

	mt := pClient.GetMetric(ns, ver)
	if mt.Err != nil {
		fmt.Println(mt.Err)
		return
	}
	metrics = append(metrics, mt.Metric)

	/*
		NAMESPACE                VERSION         LAST ADVERTISED TIME
		/intel/mock/foo          2               Wed, 09 Sep 2015 10:01:04 PDT

		  Rules for collecting /intel/mock/foo:

		     NAME        TYPE            DEFAULT         REQUIRED     MINIMUM   MAXIMUM
		     name        string          bob             false
		     password    string                          true
		     portRange   int                             false        9000      10000
	*/
	for _, metric := range metrics {
		namespace := getNamespace(metric)

		w := tabwriter.NewWriter(os.Stdout, 0, 8, 1, '\t', 0)
		printFields(w, false, 0, "NAMESPACE", "VERSION", "UNIT", "LAST ADVERTISED TIME", "DESCRIPTION")
		printFields(w, false, 0, namespace, metric.Version, metric.Unit, time.Unix(metric.LastAdvertisedTimestamp, 0).Format(time.RFC1123), metric.Description)
		w.Flush()
		if metric.Dynamic {

			//	NAMESPACE                VERSION     UNIT        LAST ADVERTISED TIME            DESCRIPTION
			//	/intel/mock/[host]/baz   2           mock unit   Wed, 09 Sep 2015 10:01:04 PDT   mock description
			//
			//	  Dynamic elements of namespace: /intel/mock/[host]/baz
			//
			//           NAME        DESCRIPTION
			//           host        name of the host
			//
			//	  Rules for collecting /intel/mock/[host]/baz:
			//
			//	     NAME        TYPE            DEFAULT         REQUIRED     MINIMUM   MAXIMUM

			fmt.Printf("\n  Dynamic elements of namespace: %s\n\n", namespace)
			printFields(w, true, 6, "NAME", "DESCRIPTION")
			for _, v := range metric.DynamicElements {
				printFields(w, true, 6, v.Name, v.Description)
			}
			w.Flush()
		}
		fmt.Printf("\n  Rules for collecting %s:\n\n", namespace)
		printFields(w, true, 6, "NAME", "TYPE", "DEFAULT", "REQUIRED", "MINIMUM", "MAXIMUM")
		for _, rule := range metric.Policy {
			printFields(w, true, 6, rule.Name, rule.Type, rule.Default, rule.Required, rule.Minimum, rule.Maximum)
		}
		w.Flush()
	}
}

func getNamespace(mt *rbody.Metric) string {
	ns := mt.Namespace
	if mt.Dynamic {
		slice := strings.Split(ns, "/")
		for _, v := range mt.DynamicElements {
			slice[v.Index+1] = "[" + v.Name + "]"
		}
		ns = strings.Join(slice, "/")
	}
	return ns
}

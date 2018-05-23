/*
Copyright 2018 jsonnet-bundler authors All rights reserved.

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
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"

	"github.com/jsonnet-bundler/jsonnet-bundler/pkg"
	"github.com/jsonnet-bundler/jsonnet-bundler/spec"
	"github.com/pkg/errors"
	"gopkg.in/alecthomas/kingpin.v2"
)

const (
	installActionName = "install"
	initActionName    = "init"
	basePath          = ".jsonnetpkg"
	srcDirName        = "src"
)

var (
	availableSubcommands = []string{
		initActionName,
		installActionName,
	}
	githubSlugRegex                   = regexp.MustCompile("github.com/([-_a-zA-Z0-9]+)/([-_a-zA-Z0-9]+)")
	githubSlugWithVersionRegex        = regexp.MustCompile("github.com/([-_a-zA-Z0-9]+)/([-_a-zA-Z0-9]+)@(.*)")
	githubSlugWithPathRegex           = regexp.MustCompile("github.com/([-_a-zA-Z0-9]+)/([-_a-zA-Z0-9]+)/(.*)")
	githubSlugWithPathAndVersionRegex = regexp.MustCompile("github.com/([-_a-zA-Z0-9]+)/([-_a-zA-Z0-9]+)/(.*)@(.*)")
)

func main() {
	os.Exit(Main())
}

func Main() int {
	cfg := struct {
		JsonnetHome string
	}{}

	a := kingpin.New(filepath.Base(os.Args[0]), "A jsonnet package manager")
	a.HelpFlag.Short('h')

	a.Flag("jsonnetpkg-home", "The directory used to cache packages in.").
		Default("vendor").StringVar(&cfg.JsonnetHome)

	initCmd := a.Command(initActionName, "Initialize a new empty jsonnetfile")

	installCmd := a.Command(installActionName, "Install all dependencies or install specific ones")
	installCmdURLs := installCmd.Arg("packages", "URLs to package to install").URLList()

	command, err := a.Parse(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, errors.Wrapf(err, "Error parsing commandline arguments"))
		a.Usage(os.Args[1:])
		return 2
	}

	switch command {
	case initCmd.FullCommand():
		return initCommand()
	case installCmd.FullCommand():
		return installCommand(cfg.JsonnetHome, *installCmdURLs...)
	default:
		installCommand(cfg.JsonnetHome)
	}

	return 0
}

func initCommand() int {
	err := ioutil.WriteFile(pkg.JsonnetFile, []byte("{}"), 0644)
	if err != nil {
		kingpin.Fatalf("Failed to write new jsonnetfile.json: %v", err)
		return 1
	}

	return 0
}

func installCommand(jsonnetHome string, urls ...*url.URL) int {
	m, err := pkg.LoadJsonnetfile(pkg.JsonnetFile)
	if err != nil {
		kingpin.Fatalf("failed to load jsonnetfile: %v", err)
		return 1
	}

	if len(urls) > 0 {
		for _, url := range urls {
			// install package specified in command
			// $ jsonnetpkg install ksonnet git@github.com:ksonnet/ksonnet-lib
			// $ jsonnetpkg install grafonnet git@github.com:grafana/grafonnet-lib grafonnet
			// $ jsonnetpkg install github.com/grafana/grafonnet-lib/grafonnet
			//
			// github.com/(slug)/(dir)

			urlString := url.String()
			if githubSlugRegex.MatchString(urlString) {
				name := ""
				user := ""
				repo := ""
				subdir := ""
				version := "master"
				if githubSlugWithPathRegex.MatchString(urlString) {
					if githubSlugWithPathAndVersionRegex.MatchString(urlString) {
						matches := githubSlugWithPathAndVersionRegex.FindStringSubmatch(urlString)
						user = matches[1]
						repo = matches[2]
						subdir = matches[3]
						version = matches[4]
						name = path.Base(subdir)
					} else {
						matches := githubSlugWithPathRegex.FindStringSubmatch(urlString)
						user = matches[1]
						repo = matches[2]
						subdir = matches[3]
						name = path.Base(subdir)
					}
				} else {
					if githubSlugWithVersionRegex.MatchString(urlString) {
						matches := githubSlugWithVersionRegex.FindStringSubmatch(urlString)
						user = matches[1]
						repo = matches[2]
						name = repo
						version = matches[3]
					} else {
						matches := githubSlugRegex.FindStringSubmatch(urlString)
						user = matches[1]
						repo = matches[2]
						name = repo
					}
				}

				newDep := spec.Dependency{
					Name: name,
					Source: spec.Source{
						GitSource: &spec.GitSource{
							Remote: fmt.Sprintf("https://github.com/%s/%s", user, repo),
							Subdir: subdir,
						},
					},
					Version: version,
				}
				oldDeps := m.Dependencies
				newDeps := []spec.Dependency{}
				oldDepReplaced := false
				for _, d := range oldDeps {
					if d.Name == newDep.Name {
						newDeps = append(newDeps, newDep)
						oldDepReplaced = true
					} else {
						newDeps = append(newDeps, d)
					}
				}

				if !oldDepReplaced {
					newDeps = append(newDeps, newDep)
				}

				m.Dependencies = newDeps
			} else {
				kingpin.Errorf("ignoring unrecognized url: %s", url)
			}
		}
	}

	srcPath := filepath.Join(jsonnetHome)
	err = os.MkdirAll(srcPath, os.ModePerm)
	if err != nil {
		kingpin.Fatalf("failed to create jsonnet home path: %v", err)
		return 3
	}

	lock, err := pkg.Install(context.TODO(), m, jsonnetHome)
	if err != nil {
		kingpin.Fatalf("failed to install: %v", err)
		return 3
	}

	b, err := json.MarshalIndent(m, "", "    ")
	if err != nil {
		kingpin.Fatalf("failed to encode jsonnet file: %v", err)
		return 3
	}

	err = ioutil.WriteFile(pkg.JsonnetFile, b, 0644)
	if err != nil {
		kingpin.Fatalf("failed to write jsonnet file: %v", err)
		return 3
	}

	b, err = json.MarshalIndent(lock, "", "    ")
	if err != nil {
		kingpin.Fatalf("failed to encode jsonnet file: %v", err)
		return 3
	}

	err = ioutil.WriteFile(pkg.JsonnetLockFile, b, 0644)
	if err != nil {
		kingpin.Fatalf("failed to write lock file: %v", err)
		return 3
	}

	return 0
}

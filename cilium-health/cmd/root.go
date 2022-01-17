// Copyright 2017-2018 Authors of Cilium
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cmd

import (
	"fmt"
	"os"
	"strings"

	clientPkg "github.com/cilium/cilium/pkg/health/client"
	"github.com/cilium/cilium/pkg/logging"
	"github.com/cilium/cilium/pkg/logging/logfields"
	"github.com/cilium/cilium/pkg/option"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
	"github.com/spf13/viper"
)

const targetName = "cilium-health"

var (
	client    *clientPkg.Client
	cmdRefDir string
	log       = logging.DefaultLogger.WithField(logfields.LogSubsys, targetName)
	logOpts   = make(map[string]string)
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   targetName,
	Short: "Cilium Health Client",
	Long:  `Client for querying the Cilium health status API`,
	Run:   run,
}

// Fatalf prints the Printf formatted message to stderr and exits the program
// Note: os.Exit(1) is not recoverable
func Fatalf(msg string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "Error: %s\n", fmt.Sprintf(msg, args...))
	os.Exit(1)
}

// Execute adds all child commands to the root command sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)
	flags := rootCmd.PersistentFlags()
	flags.BoolP("debug", "D", false, "Enable debug messages")
	flags.StringP("host", "H", "", "URI to cilium-health server API")
	flags.StringSlice("log-driver", []string{}, "Logging endpoints to use for example syslog")
	flags.Var(option.NewNamedMapOptions("log-opts", &logOpts, nil),
		"log-opt", "Log driver options for cilium-health e.g. syslog.level=info,syslog.facility=local5,syslog.tag=cilium-agent")
	viper.BindPFlags(flags)

	flags.StringVar(&cmdRefDir, "cmdref", "", "Path to cmdref output directory")
	flags.MarkHidden("cmdref")
}

// initConfig reads in config file and ENV variables if set.
func initConfig() {
	viper.SetEnvPrefix("cilium")
	viper.SetConfigName(".cilium-health") // name of config file (without extension)
	viper.AddConfigPath("$HOME")          // adding home directory as first search path

	if viper.GetBool("debug") {
		log.Level = logrus.DebugLevel
	} else {
		log.Level = logrus.InfoLevel
	}

	if cl, err := clientPkg.NewClient(viper.GetString("host")); err != nil {
		Fatalf("Error while creating client: %s\n", err)
	} else {
		client = cl
	}
}

func run(cmd *cobra.Command, args []string) {
	// Logging should always be bootstrapped first. Do not add any code above this!
	if err := logging.SetupLogging(viper.GetStringSlice("log-driver"), logging.LogOptions(logOpts), "cilium-health", viper.GetBool("debug")); err != nil {
		log.Fatal(err)
	}

	if cmdRefDir != "" {
		// Remove the line 'Auto generated by spf13/cobra on ...'
		cmd.DisableAutoGenTag = true
		if err := doc.GenMarkdownTreeCustom(cmd, cmdRefDir, filePrepend, linkHandler); err != nil {
			log.Fatal(err)
		}
		os.Exit(0)
	}

	cmd.Help()
}

func linkHandler(s string) string {
	// The generated files have a 'See also' section but the URL's are
	// hardcoded to use Markdown but we only want / have them in HTML
	// later.
	return strings.Replace(s, ".md", ".html", 1)
}

func filePrepend(s string) string {
	// Prepend a HTML comment that this file is autogenerated. So that
	// users are warned before fixing issues in the Markdown files.  Should
	// never show up on the web.
	return fmt.Sprintf("%s\n\n", "<!-- This file was autogenerated via cilium-agent --cmdref, do not edit manually-->")
}

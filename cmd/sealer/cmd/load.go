// Copyright © 2021 Alibaba Group Holding Ltd.
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
	"os"

	"github.com/sealerio/sealer/pkg/define/options"

	"github.com/sealerio/sealer/pkg/imageengine"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var loadOpts *options.LoadOptions

// loadCmd represents the load command
var loadCmd = &cobra.Command{
	Use:     "load",
	Short:   "load a ClusterImage from a tar file",
	Long:    `Load a ClusterImage from a tar archive`,
	Example: `sealer load -i kubernetes.tar`,
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		engine, err := imageengine.NewImageEngine(options.EngineGlobalConfigurations{})
		if err != nil {
			return err
		}
		return engine.Load(loadOpts)
	},
}

func init() {
	loadOpts = &options.LoadOptions{}
	flags := loadCmd.Flags()
	flags.StringVarP(&loadOpts.Input, "input", "i", "", "Load image from file")
	flags.BoolVarP(&loadOpts.Quiet, "quiet", "q", false, "Suppress the output")
	if err := loadCmd.MarkFlagRequired("input"); err != nil {
		logrus.Errorf("failed to init flag: %v", err)
		os.Exit(1)
	}
	rootCmd.AddCommand(loadCmd)
}

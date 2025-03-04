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
	"github.com/sealerio/sealer/pkg/define/options"
	"github.com/sealerio/sealer/pkg/imageengine"
	"github.com/spf13/cobra"
)

var tagCmd = &cobra.Command{
	Use:     "tag",
	Short:   "create one or more tags for local ClusterImage",
	Example: `sealer tag kubernetes:v1.19.8 firstName secondName`,
	Args:    cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		tagOpts := options.TagOptions{
			ImageNameOrID: args[0],
			Tags:          args[1:],
		}

		engine, err := imageengine.NewImageEngine(options.EngineGlobalConfigurations{})
		if err != nil {
			return err
		}
		return engine.Tag(&tagOpts)
	},
}

func init() {
	rootCmd.AddCommand(tagCmd)
}

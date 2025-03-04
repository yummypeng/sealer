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
	"github.com/sealerio/sealer/pkg/auth"
	"github.com/sealerio/sealer/pkg/define/options"
	"github.com/sealerio/sealer/pkg/imageengine"
	"github.com/spf13/cobra"

	"github.com/sealerio/sealer/pkg/image/utils"
)

var pushOpts *options.PushOptions

// pushCmd represents the push command
var pushCmd = &cobra.Command{
	Use:   "push",
	Short: "push ClusterImage to remote registry",
	// TODO: add long description.
	Long:    "",
	Example: `sealer push registry.cn-qingdao.aliyuncs.com/sealer-io/my-kubernetes-cluster-with-dashboard:latest`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		adaptor, err := imageengine.NewImageEngine(options.EngineGlobalConfigurations{})
		if err != nil {
			return err
		}
		pushOpts.Image = args[0]
		return adaptor.Push(pushOpts)
	},
	ValidArgsFunction: utils.ImageListFuncForCompletion,
}

func init() {
	pushOpts = &options.PushOptions{}

	pushCmd.Flags().StringVar(&pushOpts.Authfile, "authfile", auth.GetDefaultAuthFilePath(), "path to store auth file after login. Accessing registry with this auth.")
	// tls-verify is not working currently
	pushCmd.Flags().BoolVar(&pushOpts.TLSVerify, "tls-verify", true, "require HTTPS and verify certificates when accessing the registry. TLS verification cannot be used when talking to an insecure registry. (not work currently)")
	pushCmd.Flags().BoolVarP(&pushOpts.Quiet, "quiet", "q", false, "don't output progress information when pushing images")
	rootCmd.AddCommand(pushCmd)
}

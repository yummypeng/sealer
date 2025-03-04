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
)

var logoutConfig *options.LogoutOptions

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "logout from image registry",
	// TODO: add long description.
	Long:    "",
	Example: `sealer logout registry.cn-qingdao.aliyuncs.com`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		adaptor, err := imageengine.NewImageEngine(options.EngineGlobalConfigurations{})
		if err != nil {
			return err
		}
		logoutConfig.Domain = args[0]

		return adaptor.Logout(logoutConfig)
	},
}

func init() {
	logoutConfig = &options.LogoutOptions{}
	rootCmd.AddCommand(logoutCmd)
	logoutCmd.Flags().StringVar(&logoutConfig.Authfile, "authfile", auth.GetDefaultAuthFilePath(), "path to store auth file after login. It will be $HOME/.sealer/auth.json by default.")
}

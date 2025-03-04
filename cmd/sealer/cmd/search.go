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
	"context"
	"fmt"

	"github.com/sealerio/sealer/common"
	"github.com/sealerio/sealer/pkg/image/reference"
	save2 "github.com/sealerio/sealer/pkg/image/save"

	reference2 "github.com/distribution/distribution/v3/reference"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

const (
	imageName = "IMAGE NAME"
)

// searchCmd represents the search command
var searchCmd = &cobra.Command{
	Use:   "search",
	Short: "search ClusterImage in default registry",
	// TODO: add long description.
	Long: "",
	Example: `sealer search <imageDomain>/<imageRepo>/<imageName> ...
## default imageDomain: 'registry.cn-qingdao.aliyuncs.com', default imageRepo: 'sealer-io'
ex.:
  sealer search kubernetes seadent/rootfs docker.io/library/hello-world
`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {

		table := tablewriter.NewWriter(common.StdOut)
		table.SetHeader([]string{imageName, "version"})
		for _, imgName := range args {
			named, err := reference.ParseToNamed(imgName)
			if err != nil {
				return err
			}
			ns, err := save2.NewProxyRegistry(context.Background(), "", named.Domain())
			if err != nil {
				return err
			}
			rNamed, err := reference2.WithName(named.Repo())
			if err != nil {
				return fmt.Errorf("failed to get repository name: %v", err)
			}
			repo, err := ns.Repository(context.Background(), rNamed)
			if err != nil {
				return err
			}
			tags, err := repo.Tags(context.Background()).All(context.Background())
			if err != nil {
				return err
			}
			for _, tag := range tags {
				table.Append([]string{named.String(), tag})
			}
		}
		table.Render()
		return nil
	},
}

func init() {
	rootCmd.AddCommand(searchCmd)
}

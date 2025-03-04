// Copyright © 2022 Alibaba Group Holding Ltd.
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

package buildah

import (
	"context"

	"fmt"

	"github.com/sealerio/sealer/pkg/define/options"

	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/containers/buildah/define"
	"github.com/containers/buildah/imagebuildah"
	buildahcli "github.com/containers/buildah/pkg/cli"
	"github.com/containers/buildah/pkg/parse"
	buildahutil "github.com/containers/buildah/pkg/util"
	"github.com/containers/buildah/util"
	"github.com/containers/common/pkg/auth"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

type buildFlagsWrapper struct {
	*buildahcli.BudResults
	*buildahcli.LayerResults
	*buildahcli.FromAndBudResults
	*buildahcli.NameSpaceResults
	*buildahcli.UserNSResults
}

func (engine *Engine) Build(opts *options.BuildOptions) (string, error) {
	// The following block is to init buildah default options.
	// And call migrateFlags2BuildahBuild to set flags based on sealer build options.
	wrapper := &buildFlagsWrapper{
		BudResults:        &buildahcli.BudResults{},
		LayerResults:      &buildahcli.LayerResults{},
		FromAndBudResults: &buildahcli.FromAndBudResults{},
		NameSpaceResults:  &buildahcli.NameSpaceResults{},
		UserNSResults:     &buildahcli.UserNSResults{},
	}

	flags := engine.Flags()
	buildFlags := buildahcli.GetBudFlags(wrapper.BudResults)
	buildFlags.StringVar(&wrapper.Runtime, "runtime", util.Runtime(), "`path` to an alternate runtime. Use BUILDAH_RUNTIME environment variable to override.")

	layerFlags := buildahcli.GetLayerFlags(wrapper.LayerResults)
	fromAndBudFlags, err := buildahcli.GetFromAndBudFlags(wrapper.FromAndBudResults, wrapper.UserNSResults, wrapper.NameSpaceResults)
	if err != nil {
		return "", fmt.Errorf("failed to setup From and Build flags: %v", err)
	}

	flags.AddFlagSet(&buildFlags)
	flags.AddFlagSet(&layerFlags)
	flags.AddFlagSet(&fromAndBudFlags)
	flags.SetNormalizeFunc(buildahcli.AliasFlags)

	err = engine.migrateFlags2Wrapper(opts, wrapper)
	if err != nil {
		return "", err
	}

	options, kubefiles, err := engine.wrapper2Options(opts, wrapper)
	if err != nil {
		return "", err
	}

	return engine.build(getContext(), kubefiles, options)
}

func (engine *Engine) wrapper2Options(opts *options.BuildOptions, wrapper *buildFlagsWrapper) (define.BuildOptions, []string, error) {
	output := ""
	cleanTmpFile := false
	tags := []string{}
	if engine.Flag("tag").Changed {
		tags = wrapper.Tag
		if len(tags) > 0 {
			output = tags[0]
			tags = tags[1:]
		}
		if engine.Flag("manifest").Changed {
			for _, tag := range tags {
				if tag == wrapper.Manifest {
					return define.BuildOptions{}, []string{}, errors.New("the same name must not be specified for both '--tag' and '--manifest'")
				}
			}
		}
	}

	if err := auth.CheckAuthFile(wrapper.Authfile); err != nil {
		return define.BuildOptions{}, []string{}, err
	}
	wrapper.Authfile, cleanTmpFile =
		buildahutil.MirrorToTempFileIfPathIsDescriptor(wrapper.Authfile)
	if cleanTmpFile {
		defer os.Remove(wrapper.Authfile)
	}

	// Allow for --pull, --pull=true, --pull=false, --pull=never, --pull=always
	// --pull-always and --pull-never.  The --pull-never and --pull-always options
	// will not be documented.
	pullPolicy := define.PullIfMissing
	if strings.EqualFold(strings.TrimSpace(wrapper.Pull), "true") {
		pullPolicy = define.PullIfNewer
	}
	if wrapper.PullAlways || strings.EqualFold(strings.TrimSpace(wrapper.Pull), "always") {
		pullPolicy = define.PullAlways
	}
	if wrapper.PullNever || strings.EqualFold(strings.TrimSpace(wrapper.Pull), "never") {
		pullPolicy = define.PullNever
	}
	logrus.Debugf("Pull Policy for pull [%v]", pullPolicy)

	format, err := getImageType(wrapper.Format)
	if err != nil {
		return define.BuildOptions{}, []string{}, err
	}

	layers := buildahcli.UseLayers()
	if engine.Flag("layers").Changed {
		layers = wrapper.Layers
	}

	contextDir := opts.ContextDir

	// Nothing provided, we assume the current working directory as build
	// context
	if len(contextDir) == 0 {
		contextDir, err = os.Getwd()
		if err != nil {
			return define.BuildOptions{}, []string{}, errors.Wrapf(err, "unable to choose current working directory as build context")
		}
	} else {
		// It was local.  Use it as is.
		contextDir, err = filepath.Abs(contextDir)
		if err != nil {
			return define.BuildOptions{}, []string{}, errors.Wrapf(err, "error determining path to directory")
		}
	}

	kubefiles := getKubefiles(wrapper.File)
	if len(kubefiles) == 0 {
		kubefile, err := DiscoverKubefile(contextDir)
		if err != nil {
			return define.BuildOptions{}, []string{}, err
		}
		kubefiles = append(kubefiles, kubefile)
	}

	contextDir, err = filepath.EvalSymlinks(contextDir)
	if err != nil {
		return define.BuildOptions{}, []string{}, errors.Wrapf(err, "error evaluating symlinks in build context path")
	}

	var stdin io.Reader
	if wrapper.Stdin {
		stdin = os.Stdin
	}
	var stdout, stderr, reporter = os.Stdout, os.Stderr, os.Stderr
	if engine.Flag("logfile").Changed {
		f, err := os.OpenFile(wrapper.Logfile, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if err != nil {
			return define.BuildOptions{}, []string{}, errors.Errorf("error opening logfile %q: %v", wrapper.Logfile, err)
		}
		defer func() {
			// this will incur GoSec warning
			_ = f.Close()
		}()
		logrus.SetOutput(f)
		stdout = f
		stderr = f
		reporter = f
	}

	systemContext, err := parse.SystemContextFromOptions(engine.Command)
	if err != nil {
		return define.BuildOptions{}, []string{}, errors.Wrapf(err, "error building system context")
	}

	isolation, err := defaultIsolationOption()
	if err != nil {
		return define.BuildOptions{}, []string{}, err
	}

	runtimeFlags := []string{}
	for _, arg := range wrapper.RuntimeFlags {
		runtimeFlags = append(runtimeFlags, "--"+arg)
	}

	commonOpts, err := parse.CommonBuildOptions(engine.Command)
	if err != nil {
		return define.BuildOptions{}, []string{}, err
	}

	namespaceOptions, networkPolicy := defaultNamespaceOptions()

	usernsOption, idmappingOptions, err := parse.IDMappingOptions(engine.Command, isolation)
	if err != nil {
		return define.BuildOptions{}, []string{}, errors.Wrapf(err, "error parsing ID mapping options")
	}
	namespaceOptions.AddOrReplace(usernsOption...)

	platforms, err := parse.PlatformsFromOptions(engine.Command)
	if err != nil {
		return define.BuildOptions{}, []string{}, err
	}

	var excludes []string
	if wrapper.IgnoreFile != "" {
		if excludes, _, err = parse.ContainerIgnoreFile(contextDir, wrapper.IgnoreFile); err != nil {
			return define.BuildOptions{}, []string{}, err
		}
	}

	var timestamp *time.Time
	if engine.Command.Flag("timestamp").Changed {
		t := time.Unix(wrapper.Timestamp, 0).UTC()
		timestamp = &t
	}

	compression := define.Gzip
	if wrapper.DisableCompression {
		compression = define.Uncompressed
	}

	options := define.BuildOptions{
		AddCapabilities: wrapper.CapAdd,
		AdditionalTags:  tags,
		AllPlatforms:    wrapper.AllPlatforms,
		Annotations:     wrapper.Annotation,
		Architecture:    systemContext.ArchitectureChoice,
		//Args:                    args,
		BlobDirectory:           wrapper.BlobCache,
		CNIConfigDir:            wrapper.CNIConfigDir,
		CNIPluginPath:           wrapper.CNIPlugInPath,
		CommonBuildOpts:         commonOpts,
		Compression:             compression,
		ConfigureNetwork:        networkPolicy,
		ContextDirectory:        contextDir,
		DefaultMountsFilePath:   "",
		Devices:                 wrapper.Devices,
		DropCapabilities:        wrapper.CapDrop,
		Err:                     stderr,
		ForceRmIntermediateCtrs: wrapper.ForceRm,
		From:                    wrapper.From,
		IDMappingOptions:        idmappingOptions,
		IIDFile:                 wrapper.Iidfile,
		In:                      stdin,
		Isolation:               isolation,
		IgnoreFile:              wrapper.IgnoreFile,
		Labels:                  wrapper.Label,
		Layers:                  layers,
		LogRusage:               wrapper.LogRusage,
		Manifest:                wrapper.Manifest,
		MaxPullPushRetries:      maxPullPushRetries,
		NamespaceOptions:        namespaceOptions,
		NoCache:                 wrapper.NoCache,
		OS:                      systemContext.OSChoice,
		Out:                     stdout,
		Output:                  output,
		OutputFormat:            format,
		PullPolicy:              pullPolicy,
		PullPushRetryDelay:      pullPushRetryDelay,
		Quiet:                   wrapper.Quiet,
		RemoveIntermediateCtrs:  wrapper.Rm,
		ReportWriter:            reporter,
		Runtime:                 wrapper.Runtime,
		RuntimeArgs:             runtimeFlags,
		RusageLogFile:           wrapper.RusageLogFile,
		SignBy:                  wrapper.SignBy,
		SignaturePolicyPath:     wrapper.SignaturePolicy,
		Squash:                  wrapper.Squash,
		SystemContext:           systemContext,
		Target:                  wrapper.Target,
		TransientMounts:         wrapper.Volumes,
		Jobs:                    &wrapper.Jobs,
		Excludes:                excludes,
		Timestamp:               timestamp,
		Platforms:               platforms,
		UnsetEnvs:               wrapper.UnsetEnvs,
	}

	if wrapper.Quiet {
		options.ReportWriter = ioutil.Discard
	}

	return options, kubefiles, nil
}

func (engine *Engine) build(cxt context.Context, kubefiles []string, options define.BuildOptions) (id string, err error) {
	id, ref, err := imagebuildah.BuildDockerfiles(cxt, engine.ImageStore(), options, kubefiles...)
	if err == nil && options.Manifest != "" {
		logrus.Debugf("manifest list id = %q, ref = %q", id, ref.String())
	}
	if err != nil {
		return "", fmt.Errorf("failed to build image %v: %v", options.AdditionalTags, err)
	}

	return id, nil
}

func getKubefiles(files []string) []string {
	var kubefiles []string
	for _, f := range files {
		if f == "-" {
			kubefiles = append(kubefiles, "/dev/stdin")
		} else {
			kubefiles = append(kubefiles, f)
		}
	}
	return kubefiles
}

// this function aims to set buildah configuration based on sealer imageengine flags.
func (engine *Engine) migrateFlags2Wrapper(opts *options.BuildOptions, wrapper *buildFlagsWrapper) error {
	flags := engine.Flags()
	// imageengine cache related flags
	// cache is enabled when "layers" is true & "no-cache" is false
	err := flags.Set("layers", "true")
	if err != nil {
		return err
	}
	wrapper.Layers = !opts.NoCache
	wrapper.NoCache = opts.NoCache
	// tags. Like -t kubernetes:v1.16
	err = flags.Set("tag", strings.Join(opts.Tags, ","))
	wrapper.Tag = opts.Tags
	if err != nil {
		return err
	}
	// Hardcoded for network configuration.
	// check parse.NamespaceOptions for detailed logic.
	// this network setup for stage container, especially for RUN wget and so on.
	// so I think we can set as host network.
	err = flags.Set("network", "host")
	if err != nil {
		return err
	}

	// set platform to the flags in buildah
	// check the detail in parse.PlatformsFromOptions
	err = flags.Set("platform", opts.Platform)
	if err != nil {
		return err
	}

	wrapper.Authfile = opts.Authfile
	// do not pack kubefile into image.
	wrapper.IgnoreFile = opts.Kubefile
	wrapper.File = []string{opts.Kubefile}

	wrapper.Pull = opts.PullPolicy

	wrapper.Label = append(wrapper.Label, opts.Labels...)
	wrapper.Annotation = append(wrapper.Annotation, opts.Annotations...)
	return nil
}

// Copyright 2020 Buf Technologies, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package protoc

import (
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strings"

	"github.com/bufbuild/buf/internal/buf/bufanalysis"
	"github.com/bufbuild/buf/internal/buf/buffetch"
	"github.com/bufbuild/buf/internal/pkg/stringutil"
	"github.com/spf13/pflag"
)

const (
	includeDirPathsFlagName       = "proto_path"
	includeImportsFlagName        = "include_imports"
	includeSourceInfoFlagName     = "include_source_info"
	printFreeFieldNumbersFlagName = "print_free_field_numbers"
	outputFlagName                = "descriptor_set_out"
	pluginPathValuesFlagName      = "plugin"
	errorFormatFlagName           = "error_format"

	pluginFakeFlagName = "protoc_plugin_fake"

	encodeFlagName          = "encode"
	decodeFlagName          = "decode"
	decodeRawFlagName       = "decode_raw"
	descriptorSetInFlagName = "descriptor_set_in"
)

var (
	defaultIncludeDirPaths = []string{"."}
	defaultErrorFormat     = "gcc"
)

type flags struct {
	IncludeDirPaths       []string
	IncludeImports        bool
	IncludeSourceInfo     bool
	PrintFreeFieldNumbers bool
	Output                string
	ErrorFormat           string
}

type env struct {
	flags

	PluginNameToPluginInfo map[string]*pluginInfo
	FilePaths              []string
}

type flagsBuilder struct {
	flags

	PluginPathValues []string

	Encode          string
	Decode          string
	DecodeRaw       bool
	DescriptorSetIn []string

	pluginFake        []string
	pluginNameToValue map[string]*pluginValue
}

func newFlagsBuilder() *flagsBuilder {
	return &flagsBuilder{
		pluginNameToValue: make(map[string]*pluginValue),
	}
}

func (f *flagsBuilder) Bind(flagSet *pflag.FlagSet) {
	flagSet.StringSliceVarP(
		&f.IncludeDirPaths,
		includeDirPathsFlagName,
		"I",
		// cannot set default due to recursive flag parsing
		// no way to differentiate between default and set for now
		// perhaps we could rework pflag usage somehow
		nil,
		`The include directory paths. This is equivalent to roots in Buf.`,
	)
	flagSet.BoolVar(
		&f.IncludeImports,
		includeImportsFlagName,
		false,
		`Include imports in the resulting FileDescriptorSet.`,
	)
	flagSet.BoolVar(
		&f.IncludeSourceInfo,
		includeSourceInfoFlagName,
		false,
		`Include source info in the resulting FileDescriptorSet.`,
	)
	flagSet.BoolVar(
		&f.PrintFreeFieldNumbers,
		printFreeFieldNumbersFlagName,
		false,
		`Print the free field numbers of all messages.`,
	)
	flagSet.StringVarP(
		&f.Output,
		outputFlagName,
		"o",
		"",
		fmt.Sprintf(
			`The location to write the FileDescriptorSet. Must be one of format %s.`,
			buffetch.ImageFormatsString,
		),
	)
	flagSet.StringVar(
		&f.ErrorFormat,
		errorFormatFlagName,
		// cannot set default due to recursive flag parsing
		// no way to differentiate between default and set for now
		// perhaps we could rework pflag usage somehow
		"",
		fmt.Sprintf(
			`The error format to use. Must be one of format %s.`,
			stringutil.SliceToString(bufanalysis.AllFormatStringsWithAliases),
		),
	)
	flagSet.StringSliceVar(
		&f.PluginPathValues,
		pluginPathValuesFlagName,
		nil,
		`The paths to the plugin executables to use, either in the form "path/to/protoc-gen-foo" or "protoc-gen-foo=path/to/binary".`,
	)

	flagSet.StringSliceVar(
		&f.pluginFake,
		pluginFakeFlagName,
		nil,
		`If you are calling this, you should not be.`,
	)
	_ = flagSet.MarkHidden(pluginFakeFlagName)

	flagSet.StringVar(
		&f.Encode,
		encodeFlagName,
		"",
		`Not supported by buf.`,
	)
	_ = flagSet.MarkHidden(encodeFlagName)
	flagSet.StringVar(
		&f.Decode,
		decodeFlagName,
		"",
		`Not supported by buf.`,
	)
	_ = flagSet.MarkHidden(decodeFlagName)
	flagSet.BoolVar(
		&f.DecodeRaw,
		decodeRawFlagName,
		false,
		`Not supported by buf.`,
	)
	_ = flagSet.MarkHidden(decodeRawFlagName)
	flagSet.StringSliceVar(
		&f.DescriptorSetIn,
		descriptorSetInFlagName,
		nil,
		`Not supported by buf.`,
	)
	_ = flagSet.MarkHidden(descriptorSetInFlagName)
}

func (f *flagsBuilder) Normalize(flagSet *pflag.FlagSet, name string) string {
	if name != "descriptor_set_out" && strings.HasSuffix(name, "_out") {
		f.pluginFakeParse(name, "_out", true)
		return pluginFakeFlagName
	}
	if strings.HasSuffix(name, "_opt") {
		f.pluginFakeParse(name, "_opt", false)
		return pluginFakeFlagName
	}
	return strings.Replace(name, "-", "_", -1)
}

func (f *flagsBuilder) Build(args []string) (*env, error) {
	pluginNameToPluginInfo := make(map[string]*pluginInfo)
	seenFlagFilePaths := make(map[string]struct{})
	filePaths, err := f.buildRec(args, pluginNameToPluginInfo, seenFlagFilePaths)
	if err != nil {
		return nil, err
	}
	if err := f.checkUnsupported(); err != nil {
		return nil, err
	}
	for pluginName, pluginInfo := range pluginNameToPluginInfo {
		if pluginInfo.Out == "" && pluginInfo.Opt != "" {
			return nil, newCannotSpecifyOptWithoutOutError(pluginName)
		}
		if pluginInfo.Out == "" && pluginInfo.Path != "" {
			return nil, newCannotSpecifyPathWithoutOutError(pluginName)
		}
	}
	if len(f.IncludeDirPaths) == 0 {
		f.IncludeDirPaths = defaultIncludeDirPaths
	}
	if f.ErrorFormat == "" {
		f.ErrorFormat = defaultErrorFormat
	}
	if len(filePaths) == 0 {
		return nil, errNoInputFiles
	}
	return &env{
		flags:                  f.flags,
		PluginNameToPluginInfo: pluginNameToPluginInfo,
		FilePaths:              filePaths,
	}, nil
}

func (f *flagsBuilder) pluginFakeParse(name string, suffix string, isOut bool) {
	pluginName := strings.TrimSuffix(name, suffix)
	pluginValue, ok := f.pluginNameToValue[pluginName]
	if !ok {
		pluginValue = newPluginValue()
		f.pluginNameToValue[pluginName] = pluginValue
	}
	index := len(f.pluginFake)
	if isOut {
		pluginValue.OutIndex = index
	} else {
		pluginValue.OptIndex = index
	}
}

func (f *flagsBuilder) buildRec(
	args []string,
	pluginNameToPluginInfo map[string]*pluginInfo,
	seenFlagFilePaths map[string]struct{},
) ([]string, error) {
	if err := f.parsePluginNameToPluginInfo(pluginNameToPluginInfo); err != nil {
		return nil, err
	}
	filePaths := make([]string, 0, len(args))
	for _, arg := range args {
		if len(arg) == 0 {
			return nil, errArgEmpty
		}
		if arg[0] != '@' {
			filePaths = append(filePaths, arg)
		} else {
			flagFilePath := arg[1:]
			if _, ok := seenFlagFilePaths[flagFilePath]; ok {
				return nil, newRecursiveReferenceError(flagFilePath)
			}
			seenFlagFilePaths[flagFilePath] = struct{}{}
			data, err := ioutil.ReadFile(flagFilePath)
			if err != nil {
				return nil, err
			}
			var flagFilePathArgs []string
			for _, flagFilePathArg := range strings.Split(string(data), "\n") {
				flagFilePathArg = strings.TrimSpace(flagFilePathArg)
				if flagFilePathArg != "" {
					flagFilePathArgs = append(flagFilePathArgs, flagFilePathArg)
				}
			}
			subFlagsBuilder := newFlagsBuilder()
			flagSet := pflag.NewFlagSet(flagFilePath, pflag.ContinueOnError)
			subFlagsBuilder.Bind(flagSet)
			flagSet.SetNormalizeFunc(normalizeFunc(subFlagsBuilder.Normalize))
			if err := flagSet.Parse(flagFilePathArgs); err != nil {
				return nil, err
			}
			subFilePaths, err := subFlagsBuilder.buildRec(
				flagSet.Args(),
				pluginNameToPluginInfo,
				seenFlagFilePaths,
			)
			if err != nil {
				return nil, err
			}
			if err := f.merge(subFlagsBuilder); err != nil {
				return nil, err
			}
			filePaths = append(filePaths, subFilePaths...)
		}
	}
	return filePaths, nil
}

// we need to bind a separate flags as pflags overrides the values with defaults if you bind again
// note that pflags does not error on duplicates so we do not either
func (f *flagsBuilder) merge(subFlagsBuilder *flagsBuilder) error {
	f.IncludeDirPaths = append(f.IncludeDirPaths, subFlagsBuilder.IncludeDirPaths...)
	if subFlagsBuilder.IncludeImports {
		f.IncludeImports = true
	}
	if subFlagsBuilder.IncludeSourceInfo {
		f.IncludeSourceInfo = true
	}
	if subFlagsBuilder.PrintFreeFieldNumbers {
		f.PrintFreeFieldNumbers = true
	}
	if subFlagsBuilder.Output != "" {
		f.Output = subFlagsBuilder.Output
	}
	if subFlagsBuilder.ErrorFormat != "" {
		f.ErrorFormat = subFlagsBuilder.ErrorFormat
	}
	f.PluginPathValues = append(f.PluginPathValues, subFlagsBuilder.PluginPathValues...)
	if subFlagsBuilder.Encode != "" {
		f.Encode = subFlagsBuilder.Encode
	}
	if subFlagsBuilder.Decode != "" {
		f.Decode = subFlagsBuilder.Decode
	}
	if subFlagsBuilder.DecodeRaw {
		f.DecodeRaw = true
	}
	f.DescriptorSetIn = append(f.DescriptorSetIn, subFlagsBuilder.DescriptorSetIn...)
	return nil
}

func (f *flagsBuilder) parsePluginNameToPluginInfo(pluginNameToPluginInfo map[string]*pluginInfo) error {
	for pluginName, pluginValue := range f.pluginNameToValue {
		if pluginValue.OutIndex >= 0 {
			out := f.pluginFake[pluginValue.OutIndex]
			var opt string
			split := strings.Split(out, ":")
			switch len(split) {
			case 1:
			case 2:
				out = split[1]
				opt = split[0]
			default:
				return newOutMultipleColonsError(pluginName, out)
			}
			pluginInfo, ok := pluginNameToPluginInfo[pluginName]
			if !ok {
				pluginInfo = newPluginInfo()
				pluginNameToPluginInfo[pluginName] = pluginInfo
			}
			if pluginInfo.Out != "" {
				return newDuplicateOutError(pluginName)
			}
			pluginInfo.Out = out
			if opt != "" {
				if pluginInfo.Opt != "" {
					return newDuplicateOptError(pluginName)
				}
				pluginInfo.Opt = opt
			}
		}
		if pluginValue.OptIndex >= 0 {
			pluginInfo, ok := pluginNameToPluginInfo[pluginName]
			if !ok {
				pluginInfo = newPluginInfo()
				pluginNameToPluginInfo[pluginName] = pluginInfo
			}
			if pluginInfo.Opt != "" {
				return newDuplicateOptError(pluginName)
			}
			pluginInfo.Opt = f.pluginFake[pluginValue.OptIndex]
		}
	}
	for _, pluginPathValue := range f.PluginPathValues {
		var pluginName string
		var pluginPath string
		switch split := strings.SplitN(pluginPathValue, "=", 2); len(split) {
		case 0:
			return newPluginPathValueEmptyError()
		case 1:
			pluginName = filepath.Base(split[0])
			pluginPath = split[0]
		case 2:
			pluginName = split[0]
			pluginPath = split[1]
		default:
			return newPluginPathValueInvalidError(pluginPathValue)
		}
		if !strings.HasPrefix(pluginName, "protoc-gen-") {
			return newPluginPathNameInvalidPrefixError(pluginName)
		}
		pluginName = strings.TrimPrefix(pluginName, "protoc-gen-")
		pluginInfo, ok := pluginNameToPluginInfo[pluginName]
		if !ok {
			pluginInfo = newPluginInfo()
			pluginNameToPluginInfo[pluginName] = pluginInfo
		}
		if pluginInfo.Path != "" {
			return newDuplicatePluginPathError(pluginName)
		}
		pluginInfo.Path = pluginPath
	}
	return nil
}

func (f *flagsBuilder) checkUnsupported() error {
	if f.Encode != "" {
		return newEncodeNotSupportedError()
	}
	if f.Decode != "" {
		return newDecodeNotSupportedError()
	}
	if f.DecodeRaw {
		return newDecodeRawNotSupportedError()
	}
	if len(f.DescriptorSetIn) > 0 {
		return newDescriptorSetInNotSupportedError()
	}
	return nil
}

type pluginValue struct {
	OutIndex int
	OptIndex int
}

func newPluginValue() *pluginValue {
	return &pluginValue{
		OutIndex: -1,
		OptIndex: -1,
	}
}

func normalizeFunc(f func(*pflag.FlagSet, string) string) func(*pflag.FlagSet, string) pflag.NormalizedName {
	return func(flagSet *pflag.FlagSet, name string) pflag.NormalizedName {
		return pflag.NormalizedName(f(flagSet, name))
	}
}

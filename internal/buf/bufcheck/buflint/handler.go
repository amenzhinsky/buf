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

package buflint

import (
	"context"

	"github.com/bufbuild/buf/internal/buf/bufanalysis"
	"github.com/bufbuild/buf/internal/buf/bufcheck/internal"
	"github.com/bufbuild/buf/internal/buf/bufcore"
	"github.com/bufbuild/buf/internal/buf/bufcore/bufcoreutil"
	"github.com/bufbuild/buf/internal/pkg/protosource"
	"go.uber.org/zap"
)

const globalIgnorePrefix = "buf:lint:ignore"

type handler struct {
	logger *zap.Logger
	runner *internal.Runner
}

func newHandler(logger *zap.Logger) *handler {
	return &handler{
		logger: logger,
		runner: internal.NewRunner(logger, globalIgnorePrefix),
	}
}

func (h *handler) Check(
	ctx context.Context,
	config *Config,
	image bufcore.Image,
) ([]bufanalysis.FileAnnotation, error) {
	files, err := protosource.NewFilesUnstable(ctx, bufcoreutil.NewInputFiles(image.Files())...)
	if err != nil {
		return nil, err
	}
	return h.runner.Check(ctx, configToInternalConfig(config), nil, files)
}

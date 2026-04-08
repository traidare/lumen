// Copyright 2026 Aeneas Rekkas
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
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVersionCommand_PrintsBuildVersion(t *testing.T) {
	tests := []struct {
		name     string
		version  string
		expected string
	}{
		{name: "default dev version", version: "dev", expected: "dev\n"},
		{name: "release version", version: "0.1.0", expected: "0.1.0\n"},
		{name: "pre-release version", version: "0.0.1-alpha.4", expected: "0.0.1-alpha.4\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := buildVersion
			t.Cleanup(func() { buildVersion = original })
			buildVersion = tt.version

			buf := new(bytes.Buffer)
			rootCmd.SetOut(buf)
			rootCmd.SetErr(buf)
			rootCmd.SetArgs([]string{"version"})

			err := rootCmd.Execute()
			require.NoError(t, err)
			assert.Equal(t, tt.expected, buf.String())
		})
	}
}

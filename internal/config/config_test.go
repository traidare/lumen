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

package config

import "testing"

func TestEnvOrDefaultInt(t *testing.T) {
	t.Setenv("TEST_DIMS", "384")
	if got := EnvOrDefaultInt("TEST_DIMS", 1024); got != 384 {
		t.Fatalf("got %d, want 384", got)
	}
	if got := EnvOrDefaultInt("TEST_DIMS_UNSET", 1024); got != 1024 {
		t.Fatalf("got %d, want 1024", got)
	}
}

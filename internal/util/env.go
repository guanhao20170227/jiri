// Copyright 2015 The Vanadium Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package util

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"v.io/x/devtools/internal/envutil"
	"v.io/x/devtools/internal/tool"
)

const (
	rootEnv = "V23_ROOT"
)

// LocalManifestFile returns the path to the local manifest.
func LocalManifestFile() (string, error) {
	root, err := V23Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, ".local_manifest"), nil
}

// LocalSnapshotDir returns the path to the local snapshot directory.
func LocalSnapshotDir() (string, error) {
	root, err := V23Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, ".snapshot"), nil
}

// ManifestDir returns the path to the manifest directory.
func ManifestDir() (string, error) {
	root, err := V23Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, ".manifest", "v2"), nil
}

// ManifestFile returns the path to the manifest file with the given
// relative path.
func ManifestFile(name string) (string, error) {
	dir, err := ManifestDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// RemoteSnapshotDir returns the path to the remote snapshot directory.
func RemoteSnapshotDir() (string, error) {
	manifestDir, err := ManifestDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(manifestDir, "snapshot"), nil
}

// ResolveManifestPath resolves the given manifest name to an absolute
// path in the local filesystem.
func ResolveManifestPath(name string) (string, error) {
	if name != "" {
		if filepath.IsAbs(name) {
			return name, nil
		}
		return ManifestFile(name)
	}
	path, err := LocalManifestFile()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return ResolveManifestPath("default")
		}
		return "", fmt.Errorf("Stat(%v) failed: %v", err)
	}
	return path, nil
}

// DataDirPath returns the path to the data directory of the given tool.
func DataDirPath(ctx *tool.Context, toolName string) (string, error) {
	projects, tools, err := readManifest(ctx, false)
	if err != nil {
		return "", err
	}
	if toolName == "" {
		// If the tool name is not set, use "v23" as the default. As a
		// consequence, any manifest is assumed to specify a "v23" tool.
		toolName = "v23"
	}
	tool, ok := tools[toolName]
	if !ok {
		return "", fmt.Errorf("tool %q not found in the manifest", tool.Name)
	}
	projectName := tool.Project
	project, ok := projects[projectName]
	if !ok {
		return "", fmt.Errorf("project %q not found in the manifest", projectName)
	}
	return filepath.Join(project.Path, tool.Data), nil
}

// LoadConfig loads the tools configuration file into memory.
func LoadConfig(ctx *tool.Context) (*Config, error) {
	dataDir, err := DataDirPath(ctx, tool.Name)
	if err != nil {
		return nil, err
	}
	configPath := filepath.Join(dataDir, "conf.json")
	configBytes, err := ioutil.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("ReadFile(%v) failed: %v", configPath, err)
	}
	var config Config
	if err := json.Unmarshal(configBytes, &config); err != nil {
		return nil, fmt.Errorf("Unmarshal(%v) failed: %v", string(configBytes), err)
	}
	return &config, nil
}

// VanadiumEnvironment returns the environment variables setting for
// vanadium. The util package captures the original state of the
// relevant environment variables when the tool is initialized, and
// every invocation of this function updates this original state
// according to the current config of the v23 tool.
func VanadiumEnvironment(ctx *tool.Context, platform Platform) (*envutil.Snapshot, error) {
	env := envutil.NewSnapshotFromOS()
	root, err := V23Root()
	if err != nil {
		return nil, err
	}
	config, err := LoadConfig(ctx)
	if err != nil {
		return nil, err
	}
	setGoPath(env, root, config)
	setVdlPath(env, root, config)
	if platform.OS == "darwin" || platform.OS == "linux" {
		if err := setSyncbaseCgoEnv(env, root, platform.OS); err != nil {
			return nil, err
		}
	}
	switch {
	case platform.Arch == runtime.GOARCH && platform.OS == runtime.GOOS:
		// If setting up the environment for the host, we are done.
	case platform.Arch == "arm" && platform.OS == "linux":
		// Set up cross-compilation for arm / linux.
		if err := setArmEnv(env, platform); err != nil {
			return nil, err
		}
	case platform.Arch == "arm" && platform.OS == "android":
		// Set up cross-compilation for arm / android.
		if err := setAndroidEnv(env, platform); err != nil {
			return nil, err
		}
	case (platform.Arch == "386" || platform.Arch == "amd64p32") && platform.OS == "nacl":
		// Set up cross-compilation nacl.
		if err := setNaclEnv(env, platform); err != nil {
			return nil, err
		}
	default:
		return nil, UnsupportedPlatformErr{platform}
	}
	return env, nil
}

// VanadiumGitRepoHost returns the URL that hosts vanadium git
// repositories.
func VanadiumGitRepoHost() string {
	return "https://vanadium.googlesource.com/"
}

// V23Root returns the root of the vanadium universe.
func V23Root() (string, error) {
	root := os.Getenv(rootEnv)
	if root == "" {
		return "", fmt.Errorf("%v is not set", rootEnv)
	}
	result, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("EvalSymlinks(%v) failed: %v", root)
	}
	return result, nil
}

// setAndroidEnv sets the environment variables used for android
// cross-compilation.
func setAndroidEnv(env *envutil.Snapshot, platform Platform) error {
	root, err := V23Root()
	if err != nil {
		return err
	}
	// Set Go specific environment variables.
	env.Set("CGO_ENABLED", "1")
	env.Set("GOOS", platform.OS)
	env.Set("GOARCH", platform.Arch)
	env.Set("GOARM", strings.TrimPrefix(platform.SubArch, "v"))

	// Add the paths to vanadium cross-compilation tools to the PATH.
	path := env.GetTokens("PATH", ":")
	path = append([]string{
		filepath.Join(root, "environment", "android", "go", "bin"),
	}, path...)
	env.SetTokens("PATH", path, ":")
	return nil
}

// setArmEnv sets the environment variables used for android
// cross-compilation.
func setArmEnv(env *envutil.Snapshot, platform Platform) error {
	root, err := V23Root()
	if err != nil {
		return err
	}
	// Set Go specific environment variables.
	env.Set("GOARCH", platform.Arch)
	env.Set("GOARM", strings.TrimPrefix(platform.SubArch, "v"))
	env.Set("GOOS", platform.OS)

	// Add the paths to vanadium cross-compilation tools to the PATH.
	path := env.GetTokens("PATH", ":")
	path = append([]string{
		filepath.Join(root, "third_party", "cout", "xgcc", "cross_arm"),
		filepath.Join(root, "third_party", "repos", "go_arm", "bin"),
	}, path...)
	env.SetTokens("PATH", path, ":")
	return nil
}

// setGoPath adds the paths to vanadium Go workspaces to the GOPATH
// variable.
func setGoPath(env *envutil.Snapshot, root string, config *Config) {
	gopath := env.GetTokens("GOPATH", ":")
	// Append an entry to gopath for each vanadium go workspace.
	for _, workspace := range config.GoWorkspaces() {
		gopath = append(gopath, filepath.Join(root, workspace))
	}
	env.SetTokens("GOPATH", gopath, ":")
}

// setVdlPath adds the paths to vanadium VDL workspaces to the VDLPATH
// variable.
func setVdlPath(env *envutil.Snapshot, root string, config *Config) {
	vdlpath := env.GetTokens("VDLPATH", ":")
	// Append an entry to vdlpath for each vanadium vdl workspace.
	//
	// TODO(toddw): This logic will change when we pull vdl into a
	// separate repo.
	for _, workspace := range config.VDLWorkspaces() {
		vdlpath = append(vdlpath, filepath.Join(root, workspace))
	}
	env.SetTokens("VDLPATH", vdlpath, ":")
}

// setNaclEnv sets the environment variables used for nacl
// cross-compilation.
func setNaclEnv(env *envutil.Snapshot, platform Platform) error {
	env.Set("GOARCH", platform.Arch)
	env.Set("GOOS", platform.OS)
	return nil
}

// setSyncbaseCgoEnv sets the CGO_ENABLED variable and adds the LevelDB
// third-party C++ libraries vanadium Go code depends on to the CGO_CFLAGS and
// CGO_LDFLAGS variables.
func setSyncbaseCgoEnv(env *envutil.Snapshot, root, arch string) error {
	// Set the CGO_* variables for the vanadium syncbase component.
	env.Set("CGO_ENABLED", "1")
	cflags := env.GetTokens("CGO_CFLAGS", " ")
	ldflags := env.GetTokens("CGO_LDFLAGS", " ")
	dir := filepath.Join(root, "third_party", "cout", "leveldb")
	if _, err := os.Stat(dir); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("Stat(%v) failed: %v", dir, err)
		}
	} else {
		cflags = append(cflags, filepath.Join("-I"+dir, "include"))
		ldflags = append(ldflags, filepath.Join("-L"+dir, "lib"))
		if arch == "linux" {
			ldflags = append(ldflags, "-Wl,-rpath", filepath.Join(dir, "lib"))
		}
	}
	env.SetTokens("CGO_CFLAGS", cflags, " ")
	env.SetTokens("CGO_LDFLAGS", ldflags, " ")
	return nil
}

// BuildCopRotationPath returns the path to the build cop rotation file.
func BuildCopRotationPath(ctx *tool.Context) (string, error) {
	dataDir, err := DataDirPath(ctx, tool.Name)
	if err != nil {
		return "", err
	}
	return filepath.Join(dataDir, "buildcop.xml"), nil
}

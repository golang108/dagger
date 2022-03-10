package pkg

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/gofrs/flock"
	"github.com/rs/zerolog/log"
)

var (
	// FS contains the filesystem of the stdlib.
	//go:embed dagger.io universe.dagger.io
	FS embed.FS
)

var (
	DaggerModule   = "dagger.io"
	UniverseModule = "universe.dagger.io"

	modules = []string{
		DaggerModule,
		UniverseModule,
	}

	DaggerPackage = fmt.Sprintf("%s/dagger", DaggerModule)

	lockFilePath = "dagger.lock"
)

func Vendor(ctx context.Context, p string) error {
	if p == "" {
		p, _ = GetCueModParent()
	}

	cuePkgDir := path.Join(p, "cue.mod", "pkg")
	if err := os.MkdirAll(cuePkgDir, 0755); err != nil {
		return err
	}

	// Lock this function so no more than 1 process can run it at once.
	lockFile := path.Join(cuePkgDir, lockFilePath)
	l := flock.New(lockFile)
	if err := l.Lock(); err != nil {
		return err
	}
	defer func() {
		l.Unlock()
		os.Remove(lockFile)
	}()

	// ensure cue module is initialized
	if err := CueModInit(ctx, p, ""); err != nil {
		return err
	}

	// remove 0.1-style .gitignore files
	gitignorePath := path.Join(cuePkgDir, ".gitignore")
	if contents, err := ioutil.ReadFile(gitignorePath); err == nil {
		if strings.HasPrefix(string(contents), "# generated by dagger") {
			os.Remove(gitignorePath)
		}
	}

	// generate `.gitattributes` file
	if err := os.WriteFile(
		path.Join(cuePkgDir, ".gitattributes"),
		[]byte("# generated by dagger\n** linguist-generated=true\n"),
		0600,
	); err != nil {
		return err
	}

	log.Ctx(ctx).Debug().Str("mod", p).Msg("vendoring packages")

	// Unpack modules in a temporary directory
	unpackDir, err := os.MkdirTemp(cuePkgDir, "vendor-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(unpackDir)

	if err := extractModules(unpackDir); err != nil {
		return err
	}

	for _, module := range modules {
		// Semi-atomic swap of the module
		//
		// The following basically does:
		// $ rm -rf cue.mod/pkg/MODULE.old
		// $ mv cue.mod/pkg/MODULE cue.mod/pkg/MODULE.old
		// $ mv VENDOR/MODULE cue.mod/pkg/MODULE
		// $ rm -rf cue.mod/pkg/MODULE.old

		moduleDir := path.Join(cuePkgDir, module)
		backupModuleDir := moduleDir + ".old"
		if err := os.RemoveAll(backupModuleDir); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Rename(moduleDir, backupModuleDir); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		defer os.RemoveAll(backupModuleDir)

		if err := os.Rename(path.Join(unpackDir, module), moduleDir); err != nil {
			return err
		}
	}

	return nil
}

func extractModules(dest string) error {
	return fs.WalkDir(FS, ".", func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !entry.Type().IsRegular() {
			return nil
		}

		// Do not vendor the package's `cue.mod/pkg`
		if strings.Contains(p, "cue.mod/pkg") {
			return nil
		}

		contents, err := fs.ReadFile(FS, p)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}

		overlayPath := path.Join(dest, p)

		if err := os.MkdirAll(filepath.Dir(overlayPath), 0755); err != nil {
			return err
		}

		// Give exec permission on embedded file to freely use shell script
		// Exclude permission linter
		//nolint
		return os.WriteFile(overlayPath, contents, 0700)
	})
}

// GetCueModParent traverses the directory tree up through ancestors looking for a cue.mod folder
func GetCueModParent() (string, bool) {
	cwd, _ := os.Getwd()
	parentDir := cwd
	found := false

	for {
		if _, err := os.Stat(path.Join(parentDir, "cue.mod")); !errors.Is(err, os.ErrNotExist) {
			found = true
			break // found it!
		}

		parentDir = filepath.Dir(parentDir)

		if parentDir == string(os.PathSeparator) {
			// reached the root
			parentDir = cwd // reset to working directory
			break
		}
	}

	return parentDir, found
}

func CueModInit(ctx context.Context, parentDir, module string) error {
	lg := log.Ctx(ctx)

	absParentDir, err := filepath.Abs(parentDir)
	if err != nil {
		return err
	}

	modDir := path.Join(absParentDir, "cue.mod")
	if err := os.MkdirAll(modDir, 0755); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
	}

	modFile := path.Join(modDir, "module.cue")
	if _, err := os.Stat(modFile); err != nil {
		statErr, ok := err.(*os.PathError)
		if !ok {
			return statErr
		}

		lg.Debug().Str("mod", parentDir).Msg("initializing cue.mod")
		contents := fmt.Sprintf(`module: "%s"`, module)
		if err := os.WriteFile(modFile, []byte(contents), 0600); err != nil {
			return err
		}
	}

	if err := os.Mkdir(path.Join(modDir, "usr"), 0755); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
	}
	if err := os.Mkdir(path.Join(modDir, "pkg"), 0755); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
	}

	return nil
}

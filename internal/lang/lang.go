package lang

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type Project struct {
	Language string
	Root     string
	TestCmd  []string
}

// Detect walks up from dir looking for project markers.
func Detect(dir string) (Project, bool) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	d := abs
	for {
		if p, ok := detectAt(d); ok {
			return p, true
		}
		parent := filepath.Dir(d)
		if parent == d {
			return Project{}, false
		}
		d = parent
	}
}

func exists(d, name string) bool {
	_, err := os.Stat(filepath.Join(d, name))
	return err == nil
}

func globExists(d, pattern string) bool {
	m, _ := filepath.Glob(filepath.Join(d, pattern))
	return len(m) > 0
}

func detectAt(d string) (Project, bool) {
	switch {
	case exists(d, "go.mod"):
		return Project{"Go", d, []string{"go", "test", "./..."}}, true
	case exists(d, "Cargo.toml"):
		return Project{"Rust", d, []string{"cargo", "test"}}, true
	case exists(d, "deno.json") || exists(d, "deno.jsonc"):
		return Project{"TypeScript (Deno)", d, []string{"deno", "test"}}, true
	case exists(d, "bun.lockb") || exists(d, "bun.lock"):
		return Project{"JavaScript (Bun)", d, []string{"bun", "test"}}, true
	case exists(d, "package.json"):
		return Project{"JavaScript/TypeScript (Node)", d, []string{"npm", "test", "--silent"}}, true
	case exists(d, "pom.xml"):
		return Project{"Java (Maven)", d, []string{"mvn", "-q", "test"}}, true
	case exists(d, "build.gradle") || exists(d, "build.gradle.kts"):
		if exists(d, gradlew()) {
			return Project{"Java (Gradle)", d, []string{"./" + gradlew(), "test"}}, true
		}
		return Project{"Java (Gradle)", d, []string{"gradle", "test"}}, true
	case globExists(d, "*.csproj") || globExists(d, "*.sln"):
		return Project{"C#", d, []string{"dotnet", "test"}}, true
	case exists(d, "phpunit.xml") || exists(d, "phpunit.xml.dist"):
		if exists(d, "vendor/bin/phpunit") {
			return Project{"PHP", d, []string{"vendor/bin/phpunit"}}, true
		}
		return Project{"PHP", d, []string{"phpunit"}}, true
	case exists(d, "composer.json"):
		return Project{"PHP", d, []string{"phpunit"}}, true
	case exists(d, "Gemfile"):
		if info, err := os.Stat(filepath.Join(d, "spec")); err == nil && info.IsDir() {
			return Project{"Ruby (RSpec)", d, []string{"bundle", "exec", "rspec"}}, true
		}
		return Project{"Ruby", d, []string{"bundle", "exec", "rake", "test"}}, true
	case exists(d, "pytest.ini") || exists(d, "pyproject.toml") || exists(d, "setup.py") || exists(d, "requirements.txt"):
		return Project{"Python", d, pythonTest()}, true
	}
	return Project{}, false
}

func gradlew() string {
	if runtime.GOOS == "windows" {
		return "gradlew.bat"
	}
	return "gradlew"
}

func pythonTest() []string {
	if runtime.GOOS == "windows" {
		return []string{"python", "-m", "pytest", "-q"}
	}
	if _, err := os.Stat("/usr/bin/python3"); err == nil {
		return []string{"python3", "-m", "pytest", "-q"}
	}
	return []string{"python", "-m", "pytest", "-q"}
}

// ShortName normalizes display names for prompts.
func ShortName(full string) string {
	if i := strings.Index(full, " ("); i >= 0 {
		return full[:i]
	}
	return full
}

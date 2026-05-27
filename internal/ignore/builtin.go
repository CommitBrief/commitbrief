// SPDX-License-Identifier: GPL-3.0-or-later

package ignore

var builtinPatterns = []string{
	"*.lock",
	"package-lock.json",
	"yarn.lock",
	"pnpm-lock.yaml",
	"Cargo.lock",
	"Gemfile.lock",
	"composer.lock",
	"poetry.lock",
	"Pipfile.lock",
	"go.sum",

	"vendor/**",
	"node_modules/**",
	"Pods/**",
	"third_party/**",

	"*.pb.go",
	"*_pb2.py",
	"*_pb2_grpc.py",
	"*_generated.go",
	"*.gen.go",
	"*.gen.ts",
	"*.gen.js",

	"**/mocks/**",
	"*_mock.go",
	"*.mock.ts",

	"dist/**",
	"build/**",
	"target/**",
	"bin/**",
	"out/**",
	"coverage/**",
	"coverage.out",
	"*.test",

	".commitbrief/cache/**",

	".idea/**",
	".vscode/**",
	"*.swp",
	"*~",
	".DS_Store",
	"Thumbs.db",

	"*.min.js",
	"*.min.css",
	"*.map",

	"*.png",
	"*.jpg",
	"*.jpeg",
	"*.gif",
	"*.ico",
	"*.pdf",
	"*.zip",
	"*.tar.gz",
	"*.tgz",
}

func Builtin() *Matcher {
	return parsePatterns(builtinPatterns)
}

func BuiltinPatterns() []string {
	out := make([]string, len(builtinPatterns))
	copy(out, builtinPatterns)
	return out
}

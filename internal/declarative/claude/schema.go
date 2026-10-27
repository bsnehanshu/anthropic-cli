package claude

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anthropics/anthropic-cli/v2/internal/declarative/core"
)

// schema is the file layout `ant apply` expects: a resource as a YAML file, or
// as markdown whose body is the kind's prose field; skills as directories; and
// a directory named after a kind standing in for an explicit `type:`.
//
// Core knows none of this. It reads files and reconciles resources; which file
// means what is decided here.
type schema struct{}

// kindByDir maps a conventional directory name to the kind it holds, so
// `environments/cloud.yml` needs no `type:` field. Skills are absent: a skill
// is a directory, so a loose file inside skills/ is a README or a shared
// reference, not a resource.
var kindByDir = map[string]core.Kind{
	"environments":  KindEnvironment,
	"memory_stores": KindMemoryStore,
	"vaults":        KindVault,
	"agents":        KindAgent,
	"deployments":   KindDeployment,
}

// kindByType maps the value of an explicit `type:` field to its kind.
var kindByType = map[string]core.Kind{
	string(KindSkill):       KindSkill,
	string(KindEnvironment): KindEnvironment,
	string(KindMemoryStore): KindMemoryStore,
	string(KindVault):       KindVault,
	string(KindAgent):       KindAgent,
	string(KindDeployment):  KindDeployment,
}

// fileKinds are the kinds a filename can name; a skill is a directory. A vault
// is left out: `vault.yml` and `vault-values.yaml` are what Ansible and Helm
// call their own files, and a walk must not send those to the API.
var fileKinds = []core.Kind{KindEnvironment, KindMemoryStore, KindAgent, KindDeployment}

// defaultMarkdownKind is the kind a `.md` file becomes when nothing else says:
// a file that is mostly prose is most likely a system prompt.
const defaultMarkdownKind = KindAgent

// Classify decides what kind of resource a file declares. An explicit `type:`
// always wins; otherwise the enclosing directory name decides; otherwise a
// filename that is or starts with a kind (`agent.md`, `deployment_hourly.yml`)
// decides; otherwise a `.md` is an agent, and anything else is a refusal to
// guess.
//
// A walked path instead returns ("", nil) whenever it cannot be identified with
// confidence: a repository is full of READMEs and CI config that must not be
// mistaken for resources — including files that do not parse at all, which are
// skipped rather than reported. Only a file whose directory or filename names a
// kind is held to an unrecognized `type:`, since its path already says it meant
// to be one.
func (schema) Classify(c *core.Candidate, walked bool) (core.Kind, error) {
	if c.DecodeErr != nil {
		if walked {
			return "", nil
		}
		if inVaultsDir(c.Path) {
			// The decoder's message quotes the line it stopped on.
			return "", fmt.Errorf("%s: does not parse as YAML; the detail is withheld because a file in vaults/ may hold secrets", c.Path)
		}
		return "", c.DecodeErr
	}
	if walked && c.Markdown && !c.HasFrontmatter {
		// No frontmatter at all: prose, not a definition. This is the rule that
		// keeps agents/README.md from being created as an agent named "README"
		// whose system prompt is the README, so it has to stay ahead of the
		// directory rule below.
		return "", nil
	}

	raw, declared := c.Fields["type"]
	typeName, isString := raw.(string)
	if isString {
		if kind, known := kindByType[strings.TrimSpace(typeName)]; known {
			return kind, nil
		}
	}

	// Otherwise the directory decides, then the filename, but on a walk only
	// for a file that looks like a declaration: markdown needed frontmatter
	// (checked above), and YAML needs to be a non-empty mapping rather than a
	// list or a scalar.
	dirKind, fromDir := kindFromDir(c.Path)
	fileKind, name, fromFilename := kindFromFilename(c.Path)
	looksLikeDeclaration := (fromDir || fromFilename) && (c.Markdown || len(c.Fields) > 0)
	if walked && !looksLikeDeclaration {
		return "", nil
	}
	// vaults/ is also where Ansible and SOPS layouts keep plaintext secrets, so
	// a walk takes a file there for a vault only when it is shaped like one, and
	// passes over one that is not. Named outright, such a file is told what is
	// wrong in keys and never values, here and in buildVault.
	if walked && fromDir && dirKind == KindVault && !shapedLikeVault(c) {
		return "", nil
	}
	if declared {
		// Present but unusable. A named file, or a walked one that otherwise looks
		// like a declaration, reaches here, so name the problem rather than ignore
		// the field.
		if !isString {
			return "", fmt.Errorf("%s: `type` must be a string", c.Path)
		}
		if inVaultsDir(c.Path) {
			return "", fmt.Errorf("%s: `type` is not a recognized kind (expected one of %s)", c.Path, kindNames())
		}
		return "", fmt.Errorf("%s: unknown type %q (expected one of %s)", c.Path, typeName, kindNames())
	}
	if fromDir {
		return dirKind, nil
	}
	if fromFilename {
		// The kind was spelled in the filename, so it is not part of the
		// resource's name: deployment_hourly.md is "hourly", and a bare
		// agent.md is named for the directory it sits in.
		c.Name = name
		return fileKind, nil
	}
	if c.Markdown {
		return defaultMarkdownKind, nil
	}
	return "", fmt.Errorf(
		"%s: cannot tell what kind of resource this is — add a `type:` field (one of %s), name the file after its kind (e.g. deployment_%s), or move it into a directory named after its kind",
		c.Path, kindNames(), filepath.Base(c.Path))
}

// shapedLikeVault reports whether a file holds what a vault declares and
// nothing else: a written `display_name`, and at most `metadata` beside it.
func shapedLikeVault(c *core.Candidate) bool {
	_, named := c.Fields["display_name"]
	return named && len(unknownVaultKeys(c)) == 0
}

// inVaultsDir reports whether a file sits in a directory named for vaults,
// where no message may quote it.
func inVaultsDir(path string) bool {
	kind, ok := kindFromDir(path)
	return ok && kind == KindVault
}

// kindFromDir infers a kind from the enclosing directory's name.
func kindFromDir(path string) (core.Kind, bool) {
	kind, ok := kindByDir[strings.ToLower(filepath.Base(filepath.Dir(path)))]
	return kind, ok
}

// kindFromFilename reads a kind out of a filename that is exactly a kind
// (`agent.md`) or leads with one (`deployment_hourly.yml`, `environment-ci.yml`),
// and returns the name that leaves: the remainder, or for a bare kind the
// enclosing directory's name.
func kindFromFilename(path string) (core.Kind, string, bool) {
	base := filepath.Base(path)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	// Matching is case-insensitive, but the name keeps the user's casing.
	folded := strings.ToLower(stem)
	for _, kind := range fileKinds {
		label := string(kind)
		if folded == label {
			return kind, filepath.Base(filepath.Dir(path)), true
		}
		for _, sep := range []string{"_", "-", "."} {
			if rest, ok := strings.CutPrefix(folded, label+sep); ok && rest != "" {
				return kind, stem[len(label)+len(sep):], true
			}
		}
	}
	return "", "", false
}

// kindNames lists every kind for an error message, in the order kinds.go
// declares them. It is spelled out because ranging over kindByType would list
// them in random order.
func kindNames() string {
	return strings.Join([]string{
		string(KindSkill), string(KindEnvironment), string(KindMemoryStore), string(KindVault), string(KindAgent), string(KindDeployment),
	}, ", ")
}

// IsResourceDir reports whether a directory is a resource in its own right.
// Skills are the only kind declared that way: their content is a folder of
// files, marked by a SKILL.md at its root.
func (schema) IsResourceDir(dir string) (core.Kind, bool) {
	st, err := os.Stat(filepath.Join(dir, skillFileName))
	return KindSkill, err == nil && !st.IsDir()
}

// LoadDir reads a skill directory.
func (schema) LoadDir(dir string) (core.Kind, map[string]any, core.Payload, error) {
	body, payload, err := loadSkillDir(dir)
	return KindSkill, body, payload, err
}

// LockfileName is where `ant apply` keeps its state.
func (schema) LockfileName() string { return "claude-lock.json" }

// ResourceDirOf maps a SKILL.md to the skill it declares, so naming the file
// and naming its directory mean the same thing.
func (schema) ResourceDirOf(path string) (string, bool) {
	if strings.EqualFold(filepath.Base(path), skillFileName) {
		return filepath.Dir(path), true
	}
	return "", false
}

package claude

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/anthropics/anthropic-cli/v2/internal/declarative/core"
)

// prose says where a kind puts a markdown body. Every file-backed kind but a
// vault has one such place — an agent's system prompt, a deployment's first message, a
// store's description — so any of them can be written as `.md` (frontmatter
// for fields, body for the prose) or as `.yml` with the prose inline.
type prose struct {
	// field is the top-level field the body stands in for; declaring both is
	// contradictory rather than additive.
	field string
	// set writes the body into the request. Nil means fields[field] = body.
	set func(fields map[string]any, body string)
}

// build returns the Build function for a kind: the declared fields are the
// request body, a markdown body (if any) goes where p says, and a resource
// with no `name` takes the filename so a minimal file is a legal one.
func build(p prose) func(*core.Candidate) (map[string]any, error) {
	return func(c *core.Candidate) (map[string]any, error) {
		if body := strings.TrimSpace(string(c.Prose)); body != "" {
			if _, ok := c.Fields[p.field]; ok {
				return nil, fmt.Errorf(
					"%s: has a markdown body and also sets `%s`, which is what the body becomes in a .md file; keep one",
					c.Path, p.field)
			}
			if p.set != nil {
				p.set(c.Fields, body)
			} else {
				c.Fields[p.field] = body
			}
		}
		if _, ok := c.Fields["name"]; !ok {
			c.Fields["name"] = c.Name
		}
		return c.Fields, nil
	}
}

// vaultFields is everything a vault file may set. Other kinds pass their fields
// through so a new API field works the day it ships. A vault does not: a
// directory named vaults/ is also where Ansible and SOPS layouts keep secrets,
// and a file with any other key is far likelier one of those than a new field.
var vaultFields = []string{"display_name", "metadata"}

// vaultAnnotations are the `$` keys a vault file may carry: the ones an editor
// reads. Any other counts against the file as a key without the `$` would.
var vaultAnnotations = []string{"$schema", "$comment"}

// unknownVaultKeys lists, in order, the keys a file holds that a vault file
// may not.
func unknownVaultKeys(c *core.Candidate) []string {
	keys := slices.AppendSeq(slices.Clone(c.Annotations), maps.Keys(c.Fields))
	keys = slices.DeleteFunc(keys, func(key string) bool {
		return slices.Contains(vaultFields, key) || slices.Contains(vaultAnnotations, key)
	})
	slices.Sort(keys)
	return keys
}

// buildVault is a vault's Build: the declared fields are the request body. It
// is the one file-backed kind with no prose field, so a markdown body has
// nowhere to go, and the one that does not take its name from the file: a
// written `display_name` is what tells a vault apart from a secrets file that
// happens to sit in vaults/.
//
// Every refusal names keys and never values. On a walk Classify has already
// skipped a file in vaults/ that is not shaped like a vault, so what reaches
// here was named outright or is a vault with a mistake in it.
func buildVault(c *core.Candidate) (map[string]any, error) {
	if strings.TrimSpace(string(c.Prose)) != "" {
		return nil, fmt.Errorf(
			"%s: a vault has no field a markdown body could become; keep only the frontmatter, or write it as YAML",
			c.Path)
	}
	if _, ok := c.Fields["name"]; ok {
		return nil, fmt.Errorf("%s: a vault is named by `display_name`, not `name`", c.Path)
	}
	if unknown := unknownVaultKeys(c); len(unknown) > 0 {
		return nil, fmt.Errorf("%s: a vault takes only `display_name` and `metadata`, not `%s`", c.Path, strings.Join(unknown, "`, `"))
	}
	if name, _ := c.Fields["display_name"].(string); strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("%s: a vault must set `display_name` to a string", c.Path)
	}
	if raw, ok := c.Fields["metadata"]; ok {
		bag, isMap := raw.(map[string]any)
		if !isMap {
			return nil, fmt.Errorf("%s: a vault's `metadata` must be a map of strings", c.Path)
		}
		notStrings := keysWhere(bag, func(_ string, v any) bool { _, isString := v.(string); return !isString })
		if len(notStrings) > 0 {
			return nil, fmt.Errorf("%s: a vault's `metadata` values must be strings, unlike %s", c.Path, notStrings)
		}
	}
	return c.Fields, nil
}

// keysWhere lists the keys of m that pred picks, quoted and sorted, for an
// error message.
func keysWhere(m map[string]any, pred func(key string, value any) bool) string {
	var keys []string
	for k, v := range m {
		if pred(k, v) {
			keys = append(keys, "`"+k+"`")
		}
	}
	slices.Sort(keys)
	return strings.Join(keys, ", ")
}

// firstMessage is a deployment's prose: the body becomes the user message
// every session it starts opens with.
var firstMessage = prose{
	field: "initial_events",
	set: func(fields map[string]any, body string) {
		fields["initial_events"] = []any{map[string]any{
			"type":    "user.message",
			"content": []any{map[string]any{"type": "text", "text": body}},
		}}
	},
}

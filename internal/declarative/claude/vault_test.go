package claude

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/anthropics/anthropic-cli/v2/internal/declarative/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A vault file as a quickstart would check it in: comments, a display name and
// a metadata bag, and nothing secret.
const sentryVault = `# The vault definition. A vault is a named container for credentials that
# Anthropic holds for you and injects at session runtime.
display_name: quickstart-sentry-autofix
metadata:
  quickstart: sentry-autofix
`

func TestVaultConvergesAndUpdatesInPlace(t *testing.T) {
	h := newHarness(t, map[string]string{"vaults/sentry-autofix.yaml": sentryVault})

	h.apply()
	assert.Equal(t, []string{"create vault"}, h.client.calls)
	entry := h.lock.Resources["./vaults/sentry-autofix.yaml"]
	require.NotNil(t, entry)
	assert.Equal(t, KindVault, entry.Kind)
	assert.Equal(t, map[string]any{
		"id":           entry.ID,
		"type":         "vault",
		"display_name": "quickstart-sentry-autofix",
		"metadata":     map[string]any{"quickstart": "sentry-autofix"},
	}, h.client.objects[entry.ID], "a vault has no `name`, so none may be sent")

	h.client.calls = nil
	h.apply()
	assert.Empty(t, h.client.calls, "an unchanged file must not create a second vault")

	require.NoError(t, os.WriteFile(filepath.Join(h.root, "vaults/sentry-autofix.yaml"),
		[]byte("display_name: sentry-autofix\nmetadata:\n  owner: devrel\n"), 0o644))
	h.apply()
	assert.Equal(t, []string{"update vault"}, h.client.calls)
	assert.Equal(t, entry.ID, h.lock.Resources["./vaults/sentry-autofix.yaml"].ID)
	assert.Equal(t, "sentry-autofix", h.client.objects[entry.ID]["display_name"])
	assert.Equal(t, map[string]any{"owner": "devrel"}, h.client.objects[entry.ID]["metadata"],
		"a metadata key dropped from the file is removed, like every other kind's")
}

func TestWalkSkipsAFileInVaultsThatIsNotShapedLikeAVault(t *testing.T) {
	// vaults/ is also where Ansible and SOPS layouts keep plaintext secrets. A
	// walk says nothing about such a file, because any error would quote it:
	// the unknown-`type` message prints the value, a decode error the line.
	h := newHarness(t, map[string]string{
		"vaults/top-level.yml":  "db_password: hunter2\nslack_token: xoxb-123\n",
		"vaults/only-meta.yml":  "metadata:\n  db_password: hunter2\n",
		"vaults/typed.yml":      "type: service_account\nprivate_key: hunter2\n",
		"vaults/typed-name.yml": "type: hunter2\ndisplay_name: a\n",
		"vaults/named.yml":      "name: github\n",
		"vaults/broken.yml":     "password: {{ hunter2 }}\n\tkey: [\n",
		"vaults/real.yml":       sentryVault,
		"infra/explicit.yml":    "type: vault\ndisplay_name: b\n",
	})
	plan := h.plan()
	var keys []string
	for _, c := range plan.Changes {
		keys = append(keys, c.Key)
	}
	assert.ElementsMatch(t, []string{"./vaults/real.yml", "./infra/explicit.yml"}, keys)
	assert.Empty(t, plan.Warnings)
}

func TestVaultFileMayCarryASchemaKey(t *testing.T) {
	// A vault takes no key beyond its own two, but `$schema` is not a field.
	const file = "$schema: https://example.com/vault.json\ndisplay_name: sentry\n"
	want := map[string]any{"display_name": "sentry"}

	h := newHarness(t, map[string]string{"vaults/walked.yml": file})
	plan := h.plan()
	require.Len(t, plan.Changes, 1)
	assert.Equal(t, "./vaults/walked.yml", plan.Changes[0].Key)
	assert.Equal(t, want, plan.Changes[0].Desired)

	root := writeTree(t, map[string]string{"vaults/named.yml": file})
	l := core.NewLoader(Registry(), root, nil)
	require.NoError(t, l.Add(context.Background(), []string{filepath.Join(root, "vaults/named.yml")}))
	body, err := planBody(t, l, "./vaults/named.yml")
	require.NoError(t, err)
	assert.Equal(t, want, body)
}

func TestWalkSkipsAFileInVaultsWithAnyOtherDollarKey(t *testing.T) {
	// Only what an editor reads is let through. Another tool's `$` key says the
	// file is that tool's as plainly as a key without the `$` does.
	files := map[string]string{
		"vaults/prod.yml":      "$vault_password: hunter2\ndisplay_name: prod\nmetadata:\n  db_password: hunter2\n",
		"vaults/prod.json":     `{"$vault_password": "hunter2", "display_name": "prod"}`,
		"vaults/prod.md":       "---\n$vault_password: hunter2\ndisplay_name: prod\n---\n",
		"vaults/both.yml":      "$schema: https://example.com/vault.json\n$vault_password: hunter2\ndisplay_name: prod\n",
		"vaults/annotated.yml": "$schema: https://example.com/vault.json\n$comment: for the editor\ndisplay_name: sentry\n",
	}
	plan := newHarness(t, files).plan()
	var keys []string
	for _, c := range plan.Changes {
		keys = append(keys, c.Key)
	}
	assert.Equal(t, []string{"./vaults/annotated.yml"}, keys)
	assert.Empty(t, plan.Warnings)

	root := writeTree(t, files)
	found, err := core.NewLoader(Registry(), root, nil).Discover(root)
	require.NoError(t, err)
	assert.Equal(t, []core.Found{{Key: "./vaults/annotated.yml", Kind: KindVault}}, found, "nor is it offered for tracking")
}

func TestNamedVaultFileIsRefusedForAnyOtherDollarKey(t *testing.T) {
	for name, tc := range map[string]struct{ path, file, want string }{
		"in vaults":        {"vaults/prod.yml", "$vault_password: hunter2\ndisplay_name: prod\nmetadata:\n  db_password: hunter2\n", "not `$vault_password`"},
		"beside $schema":   {"vaults/prod.yml", "$schema: https://example.com/vault.json\n$vault_password: hunter2\ndisplay_name: prod\n", "not `$vault_password`"},
		"beside other key": {"vaults/prod.yml", "$vault_password: hunter2\ndb_password: hunter2\ndisplay_name: prod\n", "not `$vault_password`, `db_password`"},
		"nothing else":     {"vaults/prod.yml", "$vault_password: hunter2\n", "not `$vault_password`"},
		"json":             {"vaults/prod.json", `{"$vault_password": "hunter2", "display_name": "prod"}`, "not `$vault_password`"},
		"frontmatter":      {"vaults/prod.md", "---\n$vault_password: hunter2\ndisplay_name: prod\n---\n", "not `$vault_password`"},
		"typed elsewhere":  {"infra/prod.yml", "type: vault\n$vault_password: hunter2\ndisplay_name: prod\n", "not `$vault_password`"},
	} {
		t.Run(name, func(t *testing.T) {
			root := writeTree(t, map[string]string{tc.path: tc.file})
			err := core.NewLoader(Registry(), root, nil).Add(context.Background(), []string{filepath.Join(root, tc.path)})
			require.ErrorContains(t, err, "a vault takes only `display_name` and `metadata`, "+tc.want)
			assert.NotContains(t, err.Error(), "hunter2")
		})
	}
}

func TestNamedVaultFileIsRefusedByKeyNotValue(t *testing.T) {
	// Named outright, a file is held to being a vault, and told what is wrong
	// with it in keys, never values.
	for name, tc := range map[string]struct{ file, want string }{
		"other keys":      {"db_password: hunter2\nslack_token: xoxb-123\nmetadata:\n  env: prod\n", "a vault takes only `display_name` and `metadata`, not `db_password`, `slack_token`"},
		"no display_name": {"metadata:\n  db_password: hunter2\n", "a vault must set `display_name` to a string"},
		"numeric name":    {"display_name: 42\n", "a vault must set `display_name` to a string"},
		"name":            {"name: hunter2\n", "a vault is named by `display_name`, not `name`"},
		"unknown type":    {"type: hunter2\ndisplay_name: a\n", "`type` is not a recognized kind"},
		"does not parse":  {"password: {{ hunter2 }}\n\tkey: [\n", "does not parse as YAML"},
	} {
		t.Run(name, func(t *testing.T) {
			root := writeTree(t, map[string]string{"vaults/prod.yml": tc.file})
			err := core.NewLoader(Registry(), root, nil).Add(context.Background(), []string{filepath.Join(root, "vaults/prod.yml")})
			require.ErrorContains(t, err, tc.want)
			assert.NotContains(t, err.Error(), "hunter2")
			assert.NotContains(t, err.Error(), "xoxb-123")
		})
	}
}
func TestVaultMetadataMustBeAFlatBagOfStrings(t *testing.T) {
	for name, tc := range map[string]struct{ file, want string }{
		"nested": {"display_name: a\nmetadata:\n  db:\n    password: hunter2\n  team: infra\n", "`metadata` values must be strings, unlike `db`"},
		"number": {"display_name: a\nmetadata:\n  port: 5432\n", "`metadata` values must be strings, unlike `port`"},
		"scalar": {"display_name: a\nmetadata: hunter2\n", "`metadata` must be a map of strings"},
	} {
		t.Run(name, func(t *testing.T) {
			root := writeTree(t, map[string]string{"vaults/a.yml": tc.file})
			err := core.NewLoader(Registry(), root, nil).Add(context.Background(), []string{root})
			require.ErrorContains(t, err, tc.want)
			assert.NotContains(t, err.Error(), "hunter2")
		})
	}
}

func TestVaultIsRecognisedByDirectoryAndType(t *testing.T) {
	root := writeTree(t, map[string]string{
		"vaults/by-dir.yml": "display_name: a\n",
		"infra/by-type.yml": "type: vault\ndisplay_name: b\n",
	})
	l := core.NewLoader(Registry(), root, nil)
	require.NoError(t, l.Add(context.Background(), []string{root}))

	for _, key := range []string{"./vaults/by-dir.yml", "./infra/by-type.yml"} {
		src := l.Sources()[key]
		require.NotNil(t, src, key)
		assert.Equal(t, KindVault, src.Kind, key)
	}
	body, err := planBody(t, l, "./infra/by-type.yml")
	require.NoError(t, err)
	assert.NotContains(t, body, "type", "`type` picks the kind and is not sent")
}

func TestVaultIsNotInferredFromAFilename(t *testing.T) {
	// Other kinds can be named in a filename. "vault" is what Ansible and Helm
	// call their own files, and those hold secrets a walk must not upload.
	root := writeTree(t, map[string]string{
		"group_vars/all/vault.yml": "db_password: hunter2\n",
		"charts/vault-values.yaml": "server:\n  ha: true\n",
		"infra/vault_slack.yml":    "display_name: slack\n",
		"agents/reviewer.md":       "---\nmodel: m\n---\nreview\n",
	})
	l := core.NewLoader(Registry(), root, nil)
	require.NoError(t, l.Add(context.Background(), []string{root}))

	keys := make([]string, 0, len(l.Sources()))
	for k := range l.Sources() {
		keys = append(keys, k)
	}
	assert.ElementsMatch(t, []string{"./agents/reviewer.md"}, keys)

	err := core.NewLoader(Registry(), root, nil).Add(context.Background(), []string{filepath.Join(root, "infra/vault_slack.yml")})
	require.ErrorContains(t, err, "cannot tell what kind of resource this is", "named outright, it still needs `type: vault` or a vaults/ directory")
}

func TestVaultMarkdownBodyIsRefused(t *testing.T) {
	// Every other file-backed kind has a field a markdown body becomes. Dropping
	// it silently would lose whatever the author thought they were declaring.
	root := writeTree(t, map[string]string{"vaults/notes.md": "---\ndisplay_name: a\n---\nHolds the Sentry tokens.\n"})
	l := core.NewLoader(Registry(), root, nil)
	err := l.Add(context.Background(), []string{root})
	if err == nil {
		_, err = planBody(t, l, "./vaults/notes.md")
	}
	require.ErrorContains(t, err, "a vault has no field a markdown body could become")
}

func TestPruneArchivesAVaultLikeAnyOtherKind(t *testing.T) {
	h := newHarness(t, map[string]string{"vaults/sentry-autofix.yaml": sentryVault})
	h.apply()
	vaultID := h.lock.Resources["./vaults/sentry-autofix.yaml"].ID

	require.NoError(t, os.Remove(filepath.Join(h.root, "vaults/sentry-autofix.yaml")))

	plan := h.plan()
	assert.False(t, plan.HasWork(), "an orphan is reported, not silently destroyed")
	require.Len(t, plan.Warnings, 1)
	assert.Contains(t, plan.Warnings[0], "--prune")

	h.client.calls = nil
	h.apply(withPrune)
	assert.Equal(t, []string{"destroy vault " + vaultID}, h.client.calls)
	assert.NotContains(t, h.lock.Keys(), "./vaults/sentry-autofix.yaml")
}

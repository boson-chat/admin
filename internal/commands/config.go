package commands

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/boson-chat/admin/config"
	"github.com/boson-chat/admin/internal/configfile"
	"github.com/boson-chat/admin/internal/services/stackconfig"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// The stack config file (config/boson.yaml) from the command line, for
// scripts/migrate-config.sh in the stack repo:
//
//	admin export-config                       # the admin section from the env
//	<svc> export-config | admin config merge /config/boson.yaml

var configCmd = &cobra.Command{Use: "config", Short: "Work with the stack config file (config/boson.yaml)"}

var mergeOverwrite bool

var configMergeCmd = &cobra.Command{
	Use:   "merge FILE",
	Short: "Merge YAML from stdin (one or more documents) into FILE, keeping its comments",
	Long: `Reads YAML documents from stdin (e.g. each service's export-config output)
and writes every leaf key into FILE atomically. Keys FILE already has are left
alone unless --overwrite. Comments, order and unknown keys in FILE are kept.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		in, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 1<<20))
		if err != nil {
			return err
		}
		added, kept, err := mergeInto(args[0], in, mergeOverwrite)
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		for _, k := range added {
			_, _ = fmt.Fprintln(out, "set  "+k)
		}
		for _, k := range kept {
			_, _ = fmt.Fprintln(out, "kept "+k+" (already in the file; --overwrite replaces it)")
		}
		if len(added) == 0 {
			_, _ = fmt.Fprintln(out, "nothing to change")
		}
		return nil
	},
}

// leaves walks a fragment: mappings are descended, everything else
// (scalars, lists) is a value.
func leaves(n *yaml.Node, prefix []string, fn func(path []string, v *yaml.Node)) {
	if n.Kind != yaml.MappingNode {
		fn(prefix, n)
		return
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		p := append(append([]string{}, prefix...), n.Content[i].Value)
		leaves(n.Content[i+1], p, fn)
	}
}

func mergeInto(path string, in []byte, overwrite bool) (added, kept []string, err error) {
	type leaf struct {
		path []string
		v    *yaml.Node
	}
	var all []leaf
	dec := yaml.NewDecoder(bytes.NewReader(in))
	for {
		var doc yaml.Node
		if err := dec.Decode(&doc); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, nil, fmt.Errorf("reading stdin: %w", err)
		}
		if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
			continue
		}
		leaves(doc.Content[0], nil, func(p []string, v *yaml.Node) {
			if len(p) == 1 && p[0] == "version" {
				return
			}
			if len(p) == 1 || (v.Kind == yaml.ScalarNode && v.Tag == "!!null") {
				return // a bare top-level value or an empty one
			}
			v.HeadComment, v.LineComment, v.FootComment = "", "", ""
			all = append(all, leaf{p, v})
		})
	}
	if len(all) == 0 {
		return nil, nil, nil
	}
	store := configfile.Open(path, configfile.Options{Header: stackconfig.FileHeader})
	if _, p := store.Current(); p != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, p)
	}
	_, err = store.Update("", "migrate-config", func(root *yaml.Node) error {
		added, kept = nil, nil
		for _, l := range all {
			name := strings.Join(l.path, ".")
			if _, cur := configfile.Lookup(root, l.path); cur != nil && !overwrite {
				kept = append(kept, name)
				continue
			}
			if err := configfile.SetNode(root, l.path, l.v); err != nil {
				return err
			}
			added = append(added, name)
		}
		return nil
	})
	return added, kept, err
}

var exportConfigCmd = &cobra.Command{
	Use:   "export-config",
	Short: "Print the admin section of the stack config file built from the env",
	Long: `Prints the admin panel's section of config/boson.yaml (YAML) from the env
vars that were explicitly set (DIRECTORY_REGISTRATION, DIRECTORY_HOSTNAME,
TLS_CHECK_HOSTS), for scripts/migrate-config.sh.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return exportAdmin(cmd.OutOrStdout(), os.LookupEnv)
	},
}

func exportAdmin(w io.Writer, lookup func(string) (string, bool)) error {
	sec := map[string]any{}
	if v, ok := lookup("DIRECTORY_REGISTRATION"); ok && strings.TrimSpace(v) != "" {
		on := strings.EqualFold(strings.TrimSpace(v), "true") || strings.TrimSpace(v) == "1"
		sec["directory_registration"] = on
	}
	if v, ok := lookup("DIRECTORY_HOSTNAME"); ok && strings.TrimSpace(v) != "" {
		h, err := stackconfig.Hostname(v)
		if err != nil {
			return fmt.Errorf("DIRECTORY_HOSTNAME: %w", err)
		}
		sec["directory_hostname"] = h
	}
	if v, ok := lookup("TLS_CHECK_HOSTS"); ok && strings.TrimSpace(v) != "" {
		k, _ := stackconfig.KeyByName(stackconfig.KeyTLSCheckHosts)
		list, err := k.Clean(config.SplitList(v))
		if err != nil {
			return fmt.Errorf("TLS_CHECK_HOSTS: %w", err)
		}
		sec["tls_check_hosts"] = list
	}
	if len(sec) == 0 {
		_, err := fmt.Fprintln(w, "# admin: nothing set in the env")
		return err
	}
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	if err := enc.Encode(map[string]any{"admin": sec}); err != nil {
		return err
	}
	return enc.Close()
}

func init() {
	configMergeCmd.Flags().BoolVar(&mergeOverwrite, "overwrite", false, "replace keys the file already has")
	configCmd.AddCommand(configMergeCmd)
	rootCmd.AddCommand(configCmd, exportConfigCmd)
}

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/HappyLadySauce/Knowledge-Core/pkg/configcenter"
	"github.com/spf13/cobra"
)

const maximumInputSize = 2 << 20

type coordinates struct {
	namespace string
	group     string
	dataID    string
	keyID     string
	keyEnv    string
}

func main() {
	command := newCommand()
	if err := command.ExecuteContext(context.Background()); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newCommand() *cobra.Command {
	command := &cobra.Command{
		Use:           "configctl",
		Short:         "Manage encrypted Knowledge Core runtime configuration",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	command.CompletionOptions.DisableDefaultCmd = true
	command.AddCommand(newValidateCommand(), newEncryptCommand(), newDecryptCommand(), newPublishCommand())
	return command
}

func newValidateCommand() *cobra.Command {
	var input string
	command := &cobra.Command{
		Use:   "validate",
		Short: "Validate one plaintext dynamic configuration document",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			contents, err := readFile(input)
			if err != nil {
				return err
			}
			if _, err := configcenter.DecodeDynamicDocument(contents); err != nil {
				return err
			}
			return nil
		},
	}
	command.Flags().StringVarP(&input, "input", "i", "", "Plaintext YAML input file")
	_ = command.MarkFlagRequired("input")
	return command
}

func newEncryptCommand() *cobra.Command {
	var input string
	var output string
	var values coordinates
	command := &cobra.Command{
		Use:   "encrypt",
		Short: "Validate and encrypt one dynamic configuration document",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			contents, err := readFile(input)
			if err != nil {
				return err
			}
			if _, err := configcenter.DecodeDynamicDocument(contents); err != nil {
				return err
			}
			key, err := keyFromEnvironment(values.keyEnv)
			if err != nil {
				return err
			}
			encrypted, err := configcenter.Encrypt(contents, key, values.keyID, values.binding())
			if err != nil {
				return err
			}
			return writeFile(output, encrypted)
		},
	}
	command.Flags().StringVarP(&input, "input", "i", "", "Plaintext YAML input file")
	command.Flags().StringVarP(&output, "output", "o", "", "Encrypted JSON output file")
	addCoordinateFlags(command, &values)
	_ = command.MarkFlagRequired("input")
	_ = command.MarkFlagRequired("output")
	return command
}

func newDecryptCommand() *cobra.Command {
	var input string
	var output string
	var values coordinates
	command := &cobra.Command{
		Use:   "decrypt",
		Short: "Decrypt and validate one dynamic configuration document",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			contents, err := readFile(input)
			if err != nil {
				return err
			}
			key, err := keyFromEnvironment(values.keyEnv)
			if err != nil {
				return err
			}
			plaintext, err := configcenter.Decrypt(contents, key, values.keyID, values.binding())
			if err != nil {
				return err
			}
			if _, err := configcenter.DecodeDynamicDocument(plaintext); err != nil {
				return err
			}
			return writeFile(output, plaintext)
		},
	}
	command.Flags().StringVarP(&input, "input", "i", "", "Encrypted JSON input file")
	command.Flags().StringVarP(&output, "output", "o", "", "Plaintext YAML output file")
	addCoordinateFlags(command, &values)
	_ = command.MarkFlagRequired("input")
	_ = command.MarkFlagRequired("output")
	return command
}

func newPublishCommand() *cobra.Command {
	var input string
	var service string
	command := &cobra.Command{
		Use:   "publish",
		Short: "Validate and publish encrypted configuration to Nacos",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			contents, err := readFile(input)
			if err != nil {
				return err
			}
			bootstrap, err := configcenter.BootstrapFromEnvironment(service)
			if err != nil {
				return err
			}
			return configcenter.Publish(command.Context(), bootstrap, contents)
		},
	}
	command.Flags().StringVarP(&input, "input", "i", "", "Encrypted JSON input file")
	command.Flags().StringVar(&service, "service", "", "Service name used to derive the default Nacos data ID")
	_ = command.MarkFlagRequired("input")
	_ = command.MarkFlagRequired("service")
	return command
}

func addCoordinateFlags(command *cobra.Command, values *coordinates) {
	command.Flags().StringVar(&values.namespace, "namespace", "", "Nacos namespace bound into authenticated encryption")
	command.Flags().StringVar(&values.group, "group", "KNOWLEDGE_CORE", "Nacos group bound into authenticated encryption")
	command.Flags().StringVar(&values.dataID, "data-id", "", "Nacos data ID bound into authenticated encryption")
	command.Flags().StringVar(&values.keyID, "key-id", "", "Envelope key identifier")
	command.Flags().StringVar(&values.keyEnv, "key-env", "KNOWLEDGE_CORE_NACOS_KEK", "Environment variable containing the base64 AES-256 key")
	for _, name := range []string{"namespace", "data-id", "key-id"} {
		_ = command.MarkFlagRequired(name)
	}
}

func (c coordinates) binding() configcenter.Binding {
	return configcenter.Binding{
		Namespace: strings.TrimSpace(c.namespace),
		Group:     strings.TrimSpace(c.group),
		DataID:    strings.TrimSpace(c.dataID),
	}
}

func keyFromEnvironment(name string) ([]byte, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("read configuration key: environment variable name is required")
	}
	value, exists := os.LookupEnv(name)
	if !exists {
		return nil, fmt.Errorf("read configuration key: environment variable %s is required", name)
	}
	return configcenter.ParseKey(value)
}

func readFile(path string) ([]byte, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", path, err)
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, maximumInputSize+1))
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", path, err)
	}
	if len(contents) == 0 || len(contents) > maximumInputSize {
		return nil, fmt.Errorf("read %q: file size is invalid", path)
	}
	return contents, nil
}

func writeFile(path string, contents []byte) error {
	path = filepath.Clean(path)
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("create temporary output for %q: %w", path, err)
	}
	temporaryName := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryName)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("secure temporary output for %q: %w", path, err)
	}
	if _, err := temporary.Write(contents); err != nil {
		return fmt.Errorf("write temporary output for %q: %w", path, err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("flush temporary output for %q: %w", path, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary output for %q: %w", path, err)
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return fmt.Errorf("replace output %q: %w", path, err)
	}
	return nil
}

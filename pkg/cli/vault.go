package cli

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"go-ansible/pkg/variable"
)

type vaultOptions struct {
	passwordFile   string
	vaultIDs       []string
	encryptVaultID string
	askVaultPass   bool
	outputFile     string
	varName        string
	newPassFile    string
	newVaultID     string
}

// NewVaultCmd 创建 ansible-vault 命令行解析器
func NewVaultCmd(version string) *cobra.Command {
	if version == "" {
		version = "0.1.0"
	}

	var opts vaultOptions

	cmd := &cobra.Command{
		Use:   "ansible-vault [create|decrypt|edit|view|encrypt|encrypt_string|rekey] [options] [file_name]",
		Short: "encryption/decryption utility for Ansible data files",
		Long: `ansible-vault can encrypt any structured data file used by Ansible.
This can include group_vars/ or host_vars/ inventory variables, variables loaded
by include_vars or vars_files, or variable files passed on the ansible-playbook
command line with -e @some_file.yml.`,
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	createCmd := &cobra.Command{
		Use:   "create <file_name>",
		Short: "Create a new encrypted file",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return runVaultCreate(args[0], opts)
		},
	}

	encryptCmd := &cobra.Command{
		Use:   "encrypt <file_name>...",
		Short: "Encrypt existing file(s)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return runVaultEncrypt(args, opts)
		},
	}

	decryptCmd := &cobra.Command{
		Use:   "decrypt <file_name>...",
		Short: "Decrypt existing encrypted file(s)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return runVaultDecrypt(args, opts)
		},
	}

	viewCmd := &cobra.Command{
		Use:   "view <file_name>...",
		Short: "View contents of encrypted file",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return runVaultView(args, opts)
		},
	}

	editCmd := &cobra.Command{
		Use:   "edit <file_name>",
		Short: "Edit an encrypted file in place",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return runVaultEdit(args[0], opts)
		},
	}

	rekeyCmd := &cobra.Command{
		Use:   "rekey <file_name>...",
		Short: "Re-key an encrypted file with a new password",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return runVaultRekey(args, opts)
		},
	}

	encryptStringCmd := &cobra.Command{
		Use:   "encrypt_string [string_to_encrypt]",
		Short: "Encrypt a string and format as YAML inline vault",
		RunE: func(c *cobra.Command, args []string) error {
			return runVaultEncryptString(args, opts)
		},
	}

	cmd.PersistentFlags().StringVar(&opts.passwordFile, "vault-password-file", "", "vault password file")
	cmd.PersistentFlags().StringArrayVar(&opts.vaultIDs, "vault-id", nil, "the vault identity to use (can be specified multiple times)")
	cmd.PersistentFlags().StringVar(&opts.encryptVaultID, "encrypt-vault-id", "", "the vault id used to encrypt, must match one of the vault-ids")
	cmd.PersistentFlags().BoolVarP(&opts.askVaultPass, "ask-vault-pass", "p", false, "ask for vault password")
	cmd.PersistentFlags().StringVarP(&opts.outputFile, "output", "o", "", "output file name for encrypt, decrypt, view or encrypt_string")
	cmd.PersistentFlags().StringVar(&opts.newPassFile, "new-vault-password-file", "", "new vault password file for rekey")
	cmd.PersistentFlags().StringVar(&opts.newVaultID, "new-vault-id", "", "the new vault identity to use for rekey")
	encryptStringCmd.Flags().StringVarP(&opts.varName, "name", "n", "", "variable name to enclose in !vault")

	cmd.AddCommand(createCmd, encryptCmd, decryptCmd, viewCmd, editCmd, rekeyCmd, encryptStringCmd)
	return cmd
}

// RunVault 独立执行 ansible-vault
func RunVault(args []string, version string) {
	fmt.Fprintln(os.Stderr, "Reimplementation and reconstruction of the main features in Python version")
	cmd := NewVaultCmd(version)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}
}

func getVaultSecretsForDecrypt(opts vaultOptions) ([]*variable.VaultSecret, error) {
	cfg := loadCLIConfig()
	return resolveVaultSecrets(cfg, opts.passwordFile, opts.vaultIDs, opts.askVaultPass)
}

func getVaultSecretForEncrypt(opts vaultOptions, confirm bool) (*variable.VaultSecret, error) {
	cfg := loadCLIConfig()
	secrets, err := resolveVaultSecrets(cfg, opts.passwordFile, opts.vaultIDs, opts.askVaultPass)
	if err != nil {
		return nil, err
	}

	targetID := opts.encryptVaultID
	if targetID == "" && len(opts.vaultIDs) == 1 {
		id, _ := variable.ParseVaultIDString(opts.vaultIDs[0])
		targetID = id
	}

	if targetID != "" {
		for _, s := range secrets {
			if s.ID == targetID {
				return s, nil
			}
		}
	}

	if len(secrets) > 0 {
		return secrets[0], nil
	}

	pass, err := promptPassword("New Vault password: ")
	if err != nil {
		return nil, err
	}
	if confirm {
		confirmPass, err := promptPassword("Confirm New Vault password: ")
		if err != nil {
			return nil, err
		}
		if pass != confirmPass {
			return nil, fmt.Errorf("passwords do not match")
		}
	}
	return &variable.VaultSecret{ID: targetID, Password: pass}, nil
}

func runVaultCreate(filename string, opts vaultOptions) error {
	secret, err := getVaultSecretForEncrypt(opts, true)
	if err != nil {
		return err
	}

	tempFile, err := os.CreateTemp("", "ansible-vault-create-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tempPath := tempFile.Name()
	_ = tempFile.Close()
	defer os.Remove(tempPath)

	if err := openEditor(tempPath); err != nil {
		return fmt.Errorf("open editor: %w", err)
	}

	content, err := os.ReadFile(tempPath)
	if err != nil {
		return fmt.Errorf("read content from editor: %w", err)
	}
	if len(bytes.TrimSpace(content)) == 0 {
		return fmt.Errorf("file not saved; aborting")
	}

	encrypted, err := variable.EncryptVaultWithID(content, secret.Password, secret.ID)
	if err != nil {
		return fmt.Errorf("encrypt file: %w", err)
	}

	outPath := filename
	if opts.outputFile != "" {
		outPath = opts.outputFile
	}

	if err := os.WriteFile(outPath, encrypted, 0644); err != nil {
		return fmt.Errorf("write encrypted file: %w", err)
	}

	fmt.Println("Encryption successful")
	return nil
}

func runVaultEncrypt(files []string, opts vaultOptions) error {
	secret, err := getVaultSecretForEncrypt(opts, true)
	if err != nil {
		return err
	}

	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("read file %s: %w", file, err)
		}

		if variable.IsVaultData(data) {
			return fmt.Errorf("%s is already encrypted", file)
		}

		encrypted, err := variable.EncryptVaultWithID(data, secret.Password, secret.ID)
		if err != nil {
			return fmt.Errorf("encrypt %s: %w", file, err)
		}

		outPath := file
		if opts.outputFile != "" {
			outPath = opts.outputFile
		}

		if err := os.WriteFile(outPath, encrypted, 0644); err != nil {
			return fmt.Errorf("write %s: %w", outPath, err)
		}
		fmt.Printf("Encryption successful for %s\n", file)
	}
	return nil
}

func runVaultDecrypt(files []string, opts vaultOptions) error {
	secrets, err := getVaultSecretsForDecrypt(opts)
	if err != nil {
		return err
	}

	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("read file %s: %w", file, err)
		}

		if !variable.IsVaultData(data) {
			return fmt.Errorf("%s is not encrypted", file)
		}

		decrypted, _, err := variable.DecryptVaultWithSecrets(data, secrets)
		if err != nil {
			return fmt.Errorf("decrypt %s: %w", file, err)
		}

		outPath := file
		if opts.outputFile != "" {
			outPath = opts.outputFile
		}

		if err := os.WriteFile(outPath, decrypted, 0644); err != nil {
			return fmt.Errorf("write %s: %w", outPath, err)
		}
		fmt.Printf("Decryption successful for %s\n", file)
	}
	return nil
}

func runVaultView(files []string, opts vaultOptions) error {
	secrets, err := getVaultSecretsForDecrypt(opts)
	if err != nil {
		return err
	}

	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("read file %s: %w", file, err)
		}

		if !variable.IsVaultData(data) {
			return fmt.Errorf("%s is not encrypted", file)
		}

		decrypted, _, err := variable.DecryptVaultWithSecrets(data, secrets)
		if err != nil {
			return fmt.Errorf("decrypt %s: %w", file, err)
		}

		var out io.Writer = os.Stdout
		if opts.outputFile != "" {
			f, err := os.OpenFile(opts.outputFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
			if err != nil {
				return fmt.Errorf("create output file %s: %w", opts.outputFile, err)
			}
			defer f.Close()
			out = f
		}

		out.Write(decrypted)
		if !bytes.HasSuffix(decrypted, []byte("\n")) {
			fmt.Fprintln(out)
		}
	}
	return nil
}

func runVaultEdit(filename string, opts vaultOptions) error {
	secrets, err := getVaultSecretsForDecrypt(opts)
	if err != nil {
		return err
	}

	data, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("read file %s: %w", filename, err)
	}

	if !variable.IsVaultData(data) {
		return fmt.Errorf("%s is not encrypted", filename)
	}

	decrypted, matchedSecret, err := variable.DecryptVaultWithSecrets(data, secrets)
	if err != nil {
		return fmt.Errorf("decrypt %s: %w", filename, err)
	}

	tempFile, err := os.CreateTemp("", "ansible-vault-edit-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tempPath := tempFile.Name()
	if _, err := tempFile.Write(decrypted); err != nil {
		_ = tempFile.Close()
		_ = os.Remove(tempPath)
		return fmt.Errorf("write temp file: %w", err)
	}
	_ = tempFile.Close()
	defer os.Remove(tempPath)

	if err := openEditor(tempPath); err != nil {
		return fmt.Errorf("open editor: %w", err)
	}

	editedContent, err := os.ReadFile(tempPath)
	if err != nil {
		return fmt.Errorf("read edited content: %w", err)
	}

	saveVaultID := matchedSecret.ID
	if opts.encryptVaultID != "" {
		saveVaultID = opts.encryptVaultID
	}
	encrypted, err := variable.EncryptVaultWithID(editedContent, matchedSecret.Password, saveVaultID)
	if err != nil {
		return fmt.Errorf("re-encrypt file: %w", err)
	}

	if err := os.WriteFile(filename, encrypted, 0644); err != nil {
		return fmt.Errorf("save encrypted file: %w", err)
	}

	fmt.Println("Edit successful")
	return nil
}

func runVaultRekey(files []string, opts vaultOptions) error {
	oldSecrets, err := getVaultSecretsForDecrypt(opts)
	if err != nil {
		return err
	}

	var newSecret *variable.VaultSecret
	if opts.newPassFile != "" {
		sec, err := variable.LoadVaultSecret(opts.newVaultID, opts.newPassFile, nil)
		if err != nil {
			return fmt.Errorf("load new vault secret: %w", err)
		}
		newSecret = sec
	} else {
		newPass, err := promptPassword("New Vault password: ")
		if err != nil {
			return err
		}
		confirmPass, err := promptPassword("Confirm New Vault password: ")
		if err != nil {
			return err
		}
		if newPass != confirmPass {
			return fmt.Errorf("new passwords do not match")
		}
		newSecret = &variable.VaultSecret{ID: opts.newVaultID, Password: newPass}
	}

	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("read file %s: %w", file, err)
		}

		if !variable.IsVaultData(data) {
			return fmt.Errorf("%s is not encrypted", file)
		}

		decrypted, matchedSecret, err := variable.DecryptVaultWithSecrets(data, oldSecrets)
		if err != nil {
			return fmt.Errorf("decrypt %s: %w", file, err)
		}

		targetID := newSecret.ID
		if targetID == "" {
			targetID = matchedSecret.ID
		}
		rekeyed, err := variable.EncryptVaultWithID(decrypted, newSecret.Password, targetID)
		if err != nil {
			return fmt.Errorf("rekey %s: %w", file, err)
		}

		if err := os.WriteFile(file, rekeyed, 0644); err != nil {
			return fmt.Errorf("write %s: %w", file, err)
		}
		fmt.Printf("Rekey successful for %s\n", file)
	}
	return nil
}

func runVaultEncryptString(args []string, opts vaultOptions) error {
	var input string
	if len(args) > 0 {
		input = strings.Join(args, " ")
	} else {
		fmt.Fprint(os.Stderr, "Reading plaintext input from stdin. (press Ctrl+D when done):\n")
		reader := bufio.NewReader(os.Stdin)
		data, err := io.ReadAll(reader)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
		input = string(data)
	}

	secret, err := getVaultSecretForEncrypt(opts, false)
	if err != nil {
		return err
	}

	encrypted, err := variable.EncryptVaultWithID([]byte(input), secret.Password, secret.ID)
	if err != nil {
		return fmt.Errorf("encrypt string: %w", err)
	}

	formatted := variable.FormatVaultInline(encrypted, opts.varName)
	if opts.outputFile != "" {
		if err := os.WriteFile(opts.outputFile, []byte(formatted), 0644); err != nil {
			return fmt.Errorf("write output file %s: %w", opts.outputFile, err)
		}
		return nil
	}
	fmt.Print(formatted)
	return nil
}

func openEditor(filePath string) error {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		for _, candidate := range []string{"vim", "vi", "nano"} {
			if _, err := exec.LookPath(candidate); err == nil {
				editor = candidate
				break
			}
		}
	}
	if editor == "" {
		return fmt.Errorf("no editor found: set $EDITOR environment variable")
	}

	parts := strings.Fields(editor)
	parts = append(parts, filePath)

	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

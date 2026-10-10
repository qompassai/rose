package cmd

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/qompassai/rose/internal/certhelper"
)

// certOptions carries the flags shared by the cert subcommands. The
// store directory is the only shared state; everything else is a
// per-subcommand flag declared on that subcommand.
type certOptions struct {
	dir string
}

func (o *certOptions) store() (*certhelper.Store, error) {
	dir := o.dir
	if dir == "" {
		var err error
		dir, err = certhelper.DefaultDir()
		if err != nil {
			return nil, err
		}
	}
	return certhelper.NewStore(dir)
}

// newCertCmd builds `rose cert` — the certificate helper for the
// hybrid-TLS transport (security-port decision D5). It provisions a
// local CA and the server/client identities the ROSE_TLS_* settings
// name, under ~/.rose/tls by default. It never starts a server and
// never modifies an existing identity: re-issue requires the operator
// to remove the old files, and CA replacement goes through rotate-ca.
func newCertCmd() *cobra.Command {
	opts := &certOptions{}
	root := &cobra.Command{
		Use:   "cert",
		Short: "Provision TLS certificates for Rose's hybrid-TLS transport",
		Args:  cobra.ExactArgs(0),
	}
	root.PersistentFlags().StringVar(&opts.dir, "dir", "", "Certificate store directory (default ~/.rose/tls)")

	var caName string
	var caDays int
	initCA := &cobra.Command{
		Use:   "init-ca",
		Short: "Create the local certificate authority",
		Args:  cobra.ExactArgs(0),
		RunE: func(_ *cobra.Command, _ []string) error {
			store, err := opts.store()
			if err != nil {
				return err
			}
			info, err := store.InitCA(caName, caDays)
			if err != nil {
				return err
			}
			fmt.Printf("CA created: %s\n  cert: %s\n  key:  %s\n  expires: %s\n",
				info.Subject, info.CertPath, info.KeyPath, info.NotAfter.Format(time.RFC3339))
			return nil
		},
	}
	initCA.Flags().StringVar(&caName, "name", "rose-ca", "CA common name")
	initCA.Flags().IntVar(&caDays, "validity-days", certhelper.DefaultCAValidityDays, "CA validity in days")

	var hosts []string
	var serverDays int
	issueServer := &cobra.Command{
		Use:   "issue-server",
		Short: "Issue the server identity for one or more DNS names or IPs",
		Args:  cobra.ExactArgs(0),
		RunE: func(_ *cobra.Command, _ []string) error {
			store, err := opts.store()
			if err != nil {
				return err
			}
			info, err := store.IssueServer(hosts, serverDays)
			if err != nil {
				return err
			}
			printIdentity("server", info)
			return nil
		},
	}
	issueServer.Flags().StringSliceVar(&hosts, "host", nil, "Server DNS name or IP (repeatable, at least one required)")
	issueServer.Flags().IntVar(&serverDays, "validity-days", certhelper.DefaultLeafValidityDays, "Server certificate validity in days")

	var clientDays int
	issueClient := &cobra.Command{
		Use:   "issue-client NAME",
		Short: "Issue a named client identity",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			store, err := opts.store()
			if err != nil {
				return err
			}
			info, err := store.IssueClient(args[0], clientDays)
			if err != nil {
				return err
			}
			printIdentity("client", info)
			return nil
		},
	}
	issueClient.Flags().IntVar(&clientDays, "validity-days", certhelper.DefaultLeafValidityDays, "Client certificate validity in days")

	list := &cobra.Command{
		Use:   "list",
		Short: "List the certificates in the store",
		Args:  cobra.ExactArgs(0),
		RunE: func(_ *cobra.Command, _ []string) error {
			store, err := opts.store()
			if err != nil {
				return err
			}
			infos, err := store.List()
			if err != nil {
				return err
			}
			return printCertList(infos)
		},
	}

	var rotateName string
	var rotateDays int
	rotate := &cobra.Command{
		Use:   "rotate-ca",
		Short: "Rotate the CA (archives the old CA; rotation is the revocation mechanism)",
		Args:  cobra.ExactArgs(0),
		RunE: func(_ *cobra.Command, _ []string) error {
			store, err := opts.store()
			if err != nil {
				return err
			}
			rot, err := store.RotateCA(rotateName, rotateDays)
			if err != nil {
				return err
			}
			fmt.Printf("CA rotated. New CA: %s (expires %s)\n  old CA archived: %s\n  trust bundle:    %s\n",
				rot.NewCA.Subject, rot.NewCA.NotAfter.Format(time.RFC3339), rot.ArchivedDir, rot.BundlePath)
			fmt.Println("Next: distribute trust-bundle.crt as the ROSE_TLS_CLIENT_CA / ROSE_TLS_CA file,")
			fmt.Println("re-issue server and client identities under the new CA, then replace the bundle")
			fmt.Println("with ca.crt alone to complete revocation of the old CA.")
			return nil
		},
	}
	rotate.Flags().StringVar(&rotateName, "name", "rose-ca", "New CA common name")
	rotate.Flags().IntVar(&rotateDays, "validity-days", certhelper.DefaultCAValidityDays, "New CA validity in days")

	var envRole, envClient string
	env := &cobra.Command{
		Use:   "env",
		Short: "Print the ROSE_TLS_* settings for a provisioned identity",
		Args:  cobra.ExactArgs(0),
		RunE: func(_ *cobra.Command, _ []string) error {
			store, err := opts.store()
			if err != nil {
				return err
			}
			return printCertEnv(store, envRole, envClient)
		},
	}
	env.Flags().StringVar(&envRole, "role", "server", "Identity role: server or client")
	env.Flags().StringVar(&envClient, "name", "", "Client name (required with --role client)")

	root.AddCommand(initCA, issueServer, issueClient, list, rotate, env)
	return root
}

func printIdentity(kind string, info certhelper.Info) {
	fmt.Printf("%s identity issued: %s\n  cert: %s\n  key:  %s\n  expires: %s\n  sha256: %s\n",
		kind, info.Subject, info.CertPath, info.KeyPath, info.NotAfter.Format(time.RFC3339), info.SHA256)
}

func printCertList(infos []certhelper.Info) error {
	if len(infos) == 0 {
		fmt.Println("No certificates. Run `rose cert init-ca` to create a CA.")
		return nil
	}
	sorted := append([]certhelper.Info{}, infos...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].CertPath < sorted[j].CertPath })
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ROLE\tNAME\tEXPIRES\tSHA256\tCERT")
	for _, info := range sorted {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", info.Role, info.Name,
			info.NotAfter.Format("2006-01-02"), shortSHA(info.SHA256), info.CertPath)
	}
	return w.Flush()
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// printCertEnv prints shell-ready ROSE_TLS_* assignments. It only
// names files; the settings carry paths, never PEM contents.
func printCertEnv(store *certhelper.Store, role, client string) error {
	switch role {
	case "server":
		if _, err := os.Stat(store.ServerCertPath()); err != nil {
			return errors.New("no server identity in this store; run `rose cert issue-server` first")
		}
		fmt.Printf("ROSE_HOST=https://<server-host>\n")
		fmt.Printf("ROSE_TLS_CERT=%s\n", store.ServerCertPath())
		fmt.Printf("ROSE_TLS_KEY=%s\n", store.ServerKeyPath())
		fmt.Printf("ROSE_TLS_CLIENT_CA=%s\n", trustFile(store))
		return nil
	case "client":
		if client == "" {
			return errors.New("--name is required with --role client")
		}
		if _, err := os.Stat(store.ClientCertPath(client)); err != nil {
			return fmt.Errorf("no client identity %q in this store; run `rose cert issue-client %s` first", client, client)
		}
		fmt.Printf("ROSE_HOST=https://<server-host>\n")
		fmt.Printf("ROSE_TLS_CERT=%s\n", store.ClientCertPath(client))
		fmt.Printf("ROSE_TLS_KEY=%s\n", store.ClientKeyPath(client))
		fmt.Printf("ROSE_TLS_CA=%s\n", trustFile(store))
		return nil
	default:
		return fmt.Errorf("unknown role %q (want server or client)", role)
	}
}

// trustFile prefers the rotation bundle when one exists: during a
// rotation transition it is the file that trusts both CAs.
func trustFile(store *certhelper.Store) string {
	if _, err := os.Stat(store.BundlePath()); err == nil {
		return store.BundlePath()
	}
	return store.CACertPath()
}

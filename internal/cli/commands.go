package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func authCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "auth", Short: "Authentication"}
	login := &cobra.Command{
		Use:   "login",
		Short: "Log in and save an API token",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := clientFrom(cmd)
			in := bufio.NewReader(os.Stdin)
			fmt.Fprint(os.Stderr, "Username: ")
			user, _ := in.ReadString('\n')
			fmt.Fprint(os.Stderr, "Password: ")
			pw, err := term.ReadPassword(int(syscall.Stdin))
			fmt.Fprintln(os.Stderr)
			if err != nil {
				return err
			}
			var out struct {
				Token string `json:"token"`
				User  struct {
					Username string `json:"username"`
				} `json:"user"`
			}
			if err := c.Do("POST", "/api/v1/auth/login", map[string]string{
				"username": strings.TrimSpace(user),
				"password": string(pw),
			}, &out); err != nil {
				return err
			}
			if err := SaveCredentials(c.BaseURL, out.Token); err != nil {
				return err
			}
			fmt.Printf("Logged in as %s\n", out.User.Username)
			return nil
		},
	}
	cmd.AddCommand(login)
	return cmd
}

func repoCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "repo", Short: "Repositories"}
	cmd.AddCommand(&cobra.Command{
		Use: "list", Short: "List repositories",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := clientFrom(cmd)
			var repos []map[string]any
			if err := c.Do("GET", "/api/v1/repos", nil, &repos); err != nil {
				return err
			}
			for _, r := range repos {
				fmt.Println(r["full_name"])
			}
			return nil
		},
	})
	var private bool
	create := &cobra.Command{
		Use: "create [name]", Short: "Create a repository", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := clientFrom(cmd)
			var repo map[string]any
			if err := c.Do("POST", "/api/v1/repos", map[string]any{"name": args[0], "private": private}, &repo); err != nil {
				return err
			}
			fmt.Println(repo["full_name"])
			return nil
		},
	}
	create.Flags().BoolVar(&private, "private", false, "create a private repository")
	cmd.AddCommand(create)
	cmd.AddCommand(&cobra.Command{
		Use: "delete [owner/name]", Short: "Delete a repository", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return clientFrom(cmd).Do("DELETE", "/api/v1/repos/"+args[0], nil, nil)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use: "view [owner/name]", Short: "View a repository", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var repo map[string]any
			if err := clientFrom(cmd).Do("GET", "/api/v1/repos/"+args[0], nil, &repo); err != nil {
				return err
			}
			fmt.Printf("%s  private=%v  default=%s\n", repo["full_name"], repo["private"], repo["default_branch"])
			return nil
		},
	})
	return cmd
}

func prCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "pr", Short: "Pull requests"}
	var title, body, base, head, repo string
	create := &cobra.Command{
		Use: "create", Short: "Open a pull request",
		RunE: func(cmd *cobra.Command, args []string) error {
			if repo == "" {
				return fmt.Errorf("--repo owner/name required")
			}
			payload := map[string]any{"title": title, "description": body, "source_branch": head, "target_branch": base}
			var out map[string]any
			if err := clientFrom(cmd).Do("POST", "/api/v1/repos/"+repo+"/pulls", payload, &out); err != nil {
				return err
			}
			fmt.Printf("#%v %s\n", out["number"], out["title"])
			return nil
		},
	}
	create.Flags().StringVar(&repo, "repo", "", "owner/name")
	create.Flags().StringVar(&title, "title", "", "title")
	create.Flags().StringVar(&body, "body", "", "description")
	create.Flags().StringVar(&head, "head", "", "source branch")
	create.Flags().StringVar(&base, "base", "main", "target branch")
	_ = create.MarkFlagRequired("repo")
	_ = create.MarkFlagRequired("title")
	_ = create.MarkFlagRequired("head")
	cmd.AddCommand(create)

	cmd.AddCommand(&cobra.Command{
		Use: "list [owner/name]", Short: "List pull requests", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var prs []map[string]any
			if err := clientFrom(cmd).Do("GET", "/api/v1/repos/"+args[0]+"/pulls", nil, &prs); err != nil {
				return err
			}
			for _, p := range prs {
				fmt.Printf("#%v %s [%v]\n", p["number"], p["title"], p["state"])
			}
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use: "view [owner/name] [number]", Short: "View a pull request", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			var out map[string]any
			if err := clientFrom(cmd).Do("GET", "/api/v1/repos/"+args[0]+"/pulls/"+args[1], nil, &out); err != nil {
				return err
			}
			pr, _ := out["pull_request"].(map[string]any)
			fmt.Printf("#%v %s [%v]\n%s\n\n%s\n", pr["number"], pr["title"], pr["state"], pr["description"], out["diff"])
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use: "merge [owner/name] [number]", Short: "Merge a pull request", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			var out map[string]any
			if err := clientFrom(cmd).Do("POST", "/api/v1/repos/"+args[0]+"/pulls/"+args[1]+"/merge", map[string]any{}, &out); err != nil {
				return err
			}
			fmt.Printf("merged %s\n", out["merge_sha"])
			return nil
		},
	})
	return cmd
}

func ciCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "ci", Short: "CI jobs"}
	cmd.AddCommand(&cobra.Command{
		Use: "logs [owner/name] [job-id]", Short: "Show CI job logs", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			var job map[string]any
			if err := clientFrom(cmd).Do("GET", "/api/v1/repos/"+args[0]+"/ci/jobs/"+args[1], nil, &job); err != nil {
				return err
			}
			fmt.Print(job["LogText"])
			if job["LogText"] == nil {
				fmt.Print(job["log_text"])
			}
			fmt.Println()
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use: "trigger [owner/name]", Short: "Queue CI for a ref", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, _ := cmd.Flags().GetString("ref")
			path := "/api/v1/repos/" + args[0] + "/ci/trigger"
			if ref != "" {
				path += "?ref=" + ref
			}
			var out map[string]any
			if err := clientFrom(cmd).Do("POST", path, map[string]any{}, &out); err != nil {
				return err
			}
			fmt.Printf("queued sha=%v\n", out["sha"])
			return nil
		},
	})
	cmd.Commands()[1].Flags().String("ref", "", "git ref")
	return cmd
}

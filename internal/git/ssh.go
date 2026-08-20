package git

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/hokan/hokan/internal/auth"
	"github.com/hokan/hokan/internal/store"
	"golang.org/x/crypto/ssh"
)

type SSH struct {
	Addr    string
	HostKey string
	Disk    *Disk
	Access  *auth.Access
	Store   store.Store
	OnPush  func(owner, name, sha string)
	Log     *slog.Logger
}

func (s *SSH) ListenAndServe() error {
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: s.publicKeyCallback,
	}
	signer, err := loadOrCreateHostKey(s.HostKey)
	if err != nil {
		return err
	}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", s.Addr)
	if err != nil {
		return err
	}
	s.log().Info("ssh listening", "addr", s.Addr)
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go s.handleConn(conn, cfg)
	}
}

func (s *SSH) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func (s *SSH) publicKeyCallback(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	fp := ssh.FingerprintSHA256(key)
	k, err := s.Store.Users().GetSSHKeyByFingerprint(context.Background(), fp)
	if err != nil {
		return nil, fmt.Errorf("unknown key")
	}
	return &ssh.Permissions{
		Extensions: map[string]string{"user-id": k.UserID},
	}, nil
}

func (s *SSH) handleConn(nConn net.Conn, cfg *ssh.ServerConfig) {
	defer nConn.Close()
	conn, chans, reqs, err := ssh.NewServerConn(nConn, cfg)
	if err != nil {
		return
	}
	defer conn.Close()
	go ssh.DiscardRequests(reqs)
	for ch := range chans {
		if ch.ChannelType() != "session" {
			_ = ch.Reject(ssh.UnknownChannelType, "unknown")
			continue
		}
		channel, requests, err := ch.Accept()
		if err != nil {
			return
		}
		go s.handleSession(conn, channel, requests)
	}
}

func (s *SSH) handleSession(conn *ssh.ServerConn, channel ssh.Channel, requests <-chan *ssh.Request) {
	defer channel.Close()
	for req := range requests {
		if req.Type != "exec" {
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
			continue
		}
		command := parseExecPayload(req.Payload)
		if req.WantReply {
			_ = req.Reply(true, nil)
		}
		s.runGit(conn, channel, strings.TrimSpace(command))
		return
	}
}

func parseExecPayload(payload []byte) string {
	if len(payload) >= 4 {
		n := int(binary.BigEndian.Uint32(payload))
		rest := payload[4:]
		if n >= 0 && n <= len(rest) {
			return string(rest[:n])
		}
	}
	return string(payload)
}

func (s *SSH) runGit(conn *ssh.ServerConn, channel ssh.Channel, command string) {
	ctx := context.Background()
	userID := ""
	if conn.Permissions != nil {
		userID = conn.Permissions.Extensions["user-id"]
	}
	user, err := s.Store.Users().GetByID(ctx, userID)
	if err != nil {
		fmt.Fprintf(channel.Stderr(), "authentication failed\n")
		return
	}

	fields := strings.Fields(command)
	if len(fields) < 2 {
		fmt.Fprintf(channel.Stderr(), "unsupported command\n")
		return
	}
	gitCmd := fields[0]
	repoArg := strings.Trim(fields[1], "'\"")
	repoArg = strings.TrimPrefix(repoArg, "/")
	repoArg = strings.TrimSuffix(repoArg, ".git")
	parts := strings.Split(repoArg, "/")
	if len(parts) != 2 || !ValidName(parts[0]) || !ValidName(parts[1]) {
		fmt.Fprintf(channel.Stderr(), "invalid repository\n")
		return
	}
	owner, name := parts[0], parts[1]
	repo, err := s.Store.Repos().GetByOwnerName(ctx, store.OwnerUser, owner, name)
	if err != nil {
		repo, err = s.Store.Repos().GetByOwnerName(ctx, store.OwnerOrg, owner, name)
	}
	if err != nil {
		fmt.Fprintf(channel.Stderr(), "repository not found\n")
		return
	}

	writeOp := gitCmd == "git-receive-pack" || gitCmd == "git receive-pack"
	if writeOp {
		if !s.Access.CanWrite(ctx, user, repo) {
			fmt.Fprintf(channel.Stderr(), "permission denied\n")
			return
		}
	} else if !s.Access.CanRead(ctx, user, repo) {
		fmt.Fprintf(channel.Stderr(), "permission denied\n")
		return
	}

	bin := "upload-pack"
	if writeOp {
		bin = "receive-pack"
	}
	gitDir := s.Disk.Path(owner, name)
	cmd := exec.Command("git", bin, gitDir)
	cmd.Stdin = channel
	cmd.Stdout = channel
	cmd.Stderr = channel.Stderr()
	err = cmd.Run()
	code := uint32(0)
	if err != nil {
		code = 1
	}
	_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{code}))
	if writeOp && s.OnPush != nil && err == nil {
		sha, err := Run(gitDir, "rev-parse", "HEAD")
		if err == nil && sha != "" && sha != "HEAD" {
			s.OnPush(owner, name, sha)
		}
	}
	_, _ = io.Copy(io.Discard, channel)
}

func loadOrCreateHostKey(path string) (ssh.Signer, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if b, err := os.ReadFile(path); err == nil {
		return ssh.ParsePrivateKey(b)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
	pemBytes := pem.EncodeToMemory(block)
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		return nil, err
	}
	return ssh.ParsePrivateKey(pemBytes)
}

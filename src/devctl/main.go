package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type Host struct {
	ID       string `json:"id"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Key      string `json:"key"`
	Password string `json:"password"`
	Note     string `json:"note"`
}

type Config struct {
	Hosts []Host `json:"hosts"`
}

func die(f string, a ...any) { fmt.Fprintf(os.Stderr, f+"\n", a...); os.Exit(1) }

func load(path string) *Config {
	b, err := os.ReadFile(path)
	if err != nil {
		die("读不到配置 %s：%v", path, err)
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		die("配置 %s 解析失败：%v", path, err)
	}
	dir := filepath.Dir(path)
	for i := range c.Hosts {
		k := c.Hosts[i].Key
		if k != "" && !filepath.IsAbs(k) {
			c.Hosts[i].Key = filepath.Join(dir, k)
		}
		if c.Hosts[i].Port == 0 {
			c.Hosts[i].Port = 22
		}
		if c.Hosts[i].User == "" {
			c.Hosts[i].User = "root"
		}
	}
	return &c
}

func (c *Config) get(id string) *Host {
	for i := range c.Hosts {
		if c.Hosts[i].ID == id {
			return &c.Hosts[i]
		}
	}
	ids := make([]string, 0, len(c.Hosts))
	for _, h := range c.Hosts {
		ids = append(ids, h.ID)
	}
	die("未知设备 %q（可用：%s）", id, strings.Join(ids, ", "))
	return nil
}

func dial(h *Host) (*ssh.Client, error) {
	var auths []ssh.AuthMethod
	if h.Key != "" {
		if b, err := os.ReadFile(h.Key); err == nil {
			if s, err := ssh.ParsePrivateKey(b); err == nil {
				auths = append(auths, ssh.PublicKeys(s))
			} else {
				fmt.Fprintf(os.Stderr, "警告：私钥 %s 解析失败（%v），尝试密码登录\n", h.Key, err)
			}
		} else {
			fmt.Fprintf(os.Stderr, "警告：读不到私钥 %s（%v），尝试密码登录\n", h.Key, err)
		}
	}
	if h.Password != "" {
		auths = append(auths, ssh.Password(h.Password))
	}
	if len(auths) == 0 {
		return nil, fmt.Errorf("没有可用凭据（key/password 都为空或不可用）")
	}
	cfg := &ssh.ClientConfig{
		User:            h.User,
		Auth:            auths,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         25 * time.Second,
	}
	addr := fmt.Sprintf("%s:%d", h.Host, h.Port)
	return ssh.Dial("tcp", addr, cfg)
}

func runSSH(h *Host, cmd string, in io.Reader) int {
	cl, err := dial(h)
	if err != nil {
		die("连接 %s (%s:%d) 失败：%v", h.ID, h.Host, h.Port, err)
	}
	defer cl.Close()
	sess, err := cl.NewSession()
	if err != nil {
		die("开 session 失败：%v", err)
	}
	defer sess.Close()
	sess.Stdin = in
	sess.Stdout = os.Stdout
	sess.Stderr = os.Stderr
	if cmd == "" {
		cmd = "exec ${SHELL:-/bin/sh} -i"
	}
	err = sess.Run(cmd)
	if err == nil {
		return 0
	}
	if ee, ok := err.(*ssh.ExitError); ok {
		return ee.ExitStatus()
	}
	fmt.Fprintf(os.Stderr, "执行失败：%v\n", err)
	return 1
}

func sftpDo(h *Host, fn func(*sftp.Client) error) {
	cl, err := dial(h)
	if err != nil {
		die("连接 %s 失败：%v", h.ID, err)
	}
	defer cl.Close()
	sc, err := sftp.NewClient(cl)
	if err != nil {
		die("SFTP 初始化失败（远端可能没开 sftp 子系统）：%v", err)
	}
	defer sc.Close()
	if err := fn(sc); err != nil {
		die("传输失败：%v", err)
	}
}

func mkdirAll(sc *sftp.Client, p string) {
	dir := filepath.ToSlash(filepath.Dir(p))
	if dir == "" || dir == "." || dir == "/" {
		return
	}
	_ = sc.MkdirAll(dir)
}

func main() {
	cfgPath := flag.String("c", "/data/adb/codex/ssh.json", "设备清单 JSON")
	flag.Usage = func() {
		fmt.Fprint(os.Stderr, `devctl —— 手机上的 SSH 控制台（Codex CLI for Android）

  devctl ls                          列出设备
  devctl ssh <id> [命令...]           在设备上执行命令（不给命令=交互 shell）
  devctl run  <id> <本地脚本>         把本地脚本推上去执行
  devctl push <id> <本地> <远端>      上传文件
  devctl pull <id> <远端> <本地>      下载文件
  devctl ping <id>                   探活
`)
	}
	flag.Parse()
	a := flag.Args()
	if len(a) == 0 {
		flag.Usage()
		os.Exit(1)
	}
	cfg := load(*cfgPath)
	switch a[0] {
	case "ls", "list":
		for _, h := range cfg.Hosts {
			note := h.Note
			if note != "" {
				note = "  # " + note
			}
			fmt.Printf("%-10s %s@%s:%d%s\n", h.ID, h.User, h.Host, h.Port, note)
		}
	case "ssh", "sh":
		if len(a) < 2 {
			die("用法：devctl ssh <id> [命令...]")
		}
		h := cfg.get(a[1])
		os.Exit(runSSH(h, strings.Join(a[2:], " "), os.Stdin))
	case "ping":
		if len(a) < 2 {
			die("用法：devctl ping <id>")
		}
		h := cfg.get(a[1])
		os.Exit(runSSH(h, "echo PONG $(hostname) $(uname -r)", nil))
	case "run":
		if len(a) < 3 {
			die("用法：devctl run <id> <本地脚本>")
		}
		h := cfg.get(a[1])
		b, err := os.ReadFile(a[2])
		if err != nil {
			die("读不到脚本：%v", err)
		}
		remote := "/tmp/devctl-run.sh"
		sftpDo(h, func(sc *sftp.Client) error {
			w, err := sc.Create(remote)
			if err != nil {
				return err
			}
			if _, err := w.Write(b); err != nil {
				return err
			}
			return w.Close()
		})
		os.Exit(runSSH(h, "sh "+remote, nil))
	case "push":
		if len(a) < 4 {
			die("用法：devctl push <id> <本地> <远端>")
		}
		h := cfg.get(a[1])
		sftpDo(h, func(sc *sftp.Client) error {
			src, err := os.Open(a[2])
			if err != nil {
				return err
			}
			defer src.Close()
			mkdirAll(sc, a[3])
			dst, err := sc.Create(a[3])
			if err != nil {
				return err
			}
			defer dst.Close()
			n, err := io.Copy(dst, src)
			fmt.Printf("已上传 %d 字节 -> %s:%s\n", n, h.ID, a[3])
			return err
		})
	case "pull":
		if len(a) < 4 {
			die("用法：devctl pull <id> <远端> <本地>")
		}
		h := cfg.get(a[1])
		sftpDo(h, func(sc *sftp.Client) error {
			src, err := sc.Open(a[2])
			if err != nil {
				return err
			}
			defer src.Close()
			if err := os.MkdirAll(filepath.Dir(a[3]), 0o755); err != nil {
				return err
			}
			dst, err := os.Create(a[3])
			if err != nil {
				return err
			}
			defer dst.Close()
			n, err := io.Copy(dst, src)
			fmt.Printf("已下载 %d 字节 <- %s:%s\n", n, h.ID, a[2])
			return err
		})
	default:
		flag.Usage()
		os.Exit(1)
	}
}

//go:build darwin

package autostart

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const launchAgentLabel = "com.cfpilot.desktop"

func launchAgentPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist"), nil
}

// Enabled 返回当前用户是否已配置 CFPilot 登录自启。
func Enabled() bool {
	path, err := launchAgentPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// Set 开启或关闭当前用户的 CFPilot 登录自启。
//
// macOS 用 ~/Library/LaunchAgents 的 plist。写文件只表示「下次登录会拉起」；
// 已经在跑的会话不会因为这次写入立刻再启动一份。
func Set(enabled bool) error {
	path, err := launchAgentPath()
	if err != nil {
		return fmt.Errorf("定位 LaunchAgents 目录失败: %w", err)
	}
	if !enabled {
		err := os.Remove(path)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("删除登录自启项失败: %w", err)
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("定位 CFPilot 程序失败: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建 LaunchAgents 目录失败: %w", err)
	}
	content := launchAgentPlist(exe)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("写入登录自启项失败: %w", err)
	}
	return nil
}

// launchAgentPlist 生成登录时拉起 CFPilot 的 plist。
//
// --no-browser：登录时不要再抢焦点打开浏览器，托盘自己在。
func launchAgentPlist(exe string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>` + launchAgentLabel + `</string>
  <key>ProgramArguments</key>
  <array>
    <string>` + xmlEscape(exe) + `</string>
    <string>--no-browser</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
</dict>
</plist>
`
}

func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

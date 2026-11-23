//go:build darwin

package mesh

import "fmt"

// CreateTunDevice 在 macOS 上明确拒绝创建虚拟网卡。
//
// 普通版（部署 / 优选 / 盲中继）不经过这里。+mesh 的数据面需要 utun，
// 目前没有可在 Linux 上交叉编译、又能在真机上验证的实现，
// 所以不假装组网已经可用。
func CreateTunDevice(name string, ipv4 string, mtu int) (TunDevice, error) {
	return nil, fmt.Errorf("mesh tun: macOS 虚拟网卡尚未实现（%s %s mtu %d）", name, ipv4, mtu)
}

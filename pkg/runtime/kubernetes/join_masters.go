// Copyright © 2022 Alibaba Group Holding Ltd.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package kubernetes

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sealerio/sealer/common"
	"github.com/sealerio/sealer/pkg/clustercert"
	"github.com/sealerio/sealer/pkg/ipvs"
	"github.com/sealerio/sealer/pkg/runtime"
	utilsnet "github.com/sealerio/sealer/utils/net"
	"github.com/sealerio/sealer/utils/ssh"
	versionUtils "github.com/sealerio/sealer/utils/version"
	"github.com/sealerio/sealer/utils/yaml"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"

	"sync"

	"github.com/pkg/errors"
)

const (
	V1991 = "v1.19.1"
	V1992 = "v1.19.2"
	V1150 = "v1.15.0"
	V1200 = "v1.20.0"
	V1230 = "v1.23.0"
)

const (
	RemoteAddEtcHosts           = "cat /etc/hosts |grep '%s' || echo '%s' >> /etc/hosts"
	RemoteUpdateEtcHosts        = `sed "s/%s/%s/g" < /etc/hosts > hosts && cp -f hosts /etc/hosts`
	RemoteCopyKubeConfig        = `rm -rf .kube/config && mkdir -p /root/.kube && cp /etc/kubernetes/admin.conf /root/.kube/config`
	RemoteNonRootCopyKubeConfig = `rm -rf ${HOME}/.kube/config && mkdir -p ${HOME}/.kube && cp /etc/kubernetes/admin.conf ${HOME}/.kube/config && chown $(id -u):$(id -g) ${HOME}/.kube/config`
	RemoteReplaceKubeConfig     = `grep -qF "apiserver.cluster.local" %s  && sed -i 's/apiserver.cluster.local/%s/' %s && sed -i 's/apiserver.cluster.local/%s/' %s`
	RemoteJoinMasterConfig      = `echo "%s" > %s/etc/kubeadm.yml`
	InitMaster115Lower          = `kubeadm init --config=%s/etc/kubeadm.yml --experimental-upload-certs`
	JoinMaster115Lower          = "kubeadm join %s:6443 --token %s --discovery-token-ca-cert-hash %s --experimental-control-plane --certificate-key %s"
	JoinNode115Lower            = "kubeadm join %s:6443 --token %s --discovery-token-ca-cert-hash %s"
	InitMaser115Upper           = `kubeadm init --config=%s/etc/kubeadm.yml --upload-certs`
	JoinMaster115Upper          = "kubeadm join --config=%s/etc/kubeadm.yml"
	JoinNode115Upper            = "kubeadm join --config=%s/etc/kubeadm.yml"
	RemoveKubeConfig            = "rm -rf /usr/bin/kube* && rm -rf ~/.kube/"
	RemoteCleanMasterOrNode     = `if which kubeadm;then kubeadm reset -f %s;fi && \
modprobe -r ipip  && lsmod && \
rm -rf /etc/kubernetes/ && \
rm -rf /etc/systemd/system/kubelet.service.d && rm -rf /etc/systemd/system/kubelet.service && \
rm -rf /usr/bin/kubeadm && rm -rf /usr/bin/kubelet-pre-start.sh && \
rm -rf /usr/bin/kubelet && rm -rf /usr/bin/crictl && \
rm -rf /etc/cni && rm -rf /opt/cni && \
rm -rf /var/lib/etcd && rm -rf /var/etcd 
`
	RemoteRemoveAPIServerEtcHost = "sed -i \"/%s/d\" /etc/hosts"
	RemoteRemoveRegistryCerts    = "rm -rf " + DockerCertDir + "/%s*"
	RemoveLvscareStaticPod       = "rm -rf  /etc/kubernetes/manifests/kube-sealyun-lvscare*"
	CreateLvscareStaticPod       = "mkdir -p /etc/kubernetes/manifests && echo '%s' > /etc/kubernetes/manifests/kube-sealyun-lvscare.yaml"
	KubeDeleteNode               = "kubectl delete node %s"
	// TODO check kubernetes certs
	RemoteCheckCerts = "kubeadm alpha certs check-expiration"
)

const (
	AdminConf      = "admin.conf"
	ControllerConf = "controller-manager.conf"
	SchedulerConf  = "scheduler.conf"
	KubeletConf    = "kubelet.conf"

	// kube file
	KUBECONTROLLERCONFIGFILE = "/etc/kubernetes/controller-manager.conf"
	KUBESCHEDULERCONFIGFILE  = "/etc/kubernetes/scheduler.conf"

	// CriSocket
	DefaultDockerCRISocket     = "/var/run/dockershim.sock"
	DefaultContainerdCRISocket = "/run/containerd/containerd.sock"
	DefaultSystemdCgroupDriver = "systemd"
	DefaultCgroupDriver        = "cgroupfs"

	// kubeadm api version
	KubeadmV1beta1 = "kubeadm.k8s.io/v1beta1"
	KubeadmV1beta2 = "kubeadm.k8s.io/v1beta2"
	KubeadmV1beta3 = "kubeadm.k8s.io/v1beta3"
)

const (
	Version     = "Version"
	EtcdServers = "etcd-servers"
)

type CommandType string

const InitMaster CommandType = "initMaster"
const JoinMaster CommandType = "joinMaster"
const JoinNode CommandType = "joinNode"

func getAPIServerHost(ipAddr net.IP, APIServer string) (host string) {
	return fmt.Sprintf("%s %s", ipAddr.String(), APIServer)
}

func (k *Runtime) JoinMasterCommands(master net.IP, joinCmd, hostname string) []string {
	apiServerHost := getAPIServerHost(k.cluster.GetMaster0IP(), k.getAPIServerDomain())
	cmdAddRegistryHosts := k.addRegistryDomainToHosts()
	certCMD := runtime.RemoteCerts(k.getCertSANS(), master, hostname, k.getSvcCIDR(), "")
	cmdAddHosts := fmt.Sprintf(RemoteAddEtcHosts, apiServerHost, apiServerHost)
	if k.RegConfig.Domain != SeaHub {
		cmdAddSeaHubHosts := fmt.Sprintf(RemoteAddEtcHosts, k.RegConfig.IP.String()+" "+SeaHub, k.RegConfig.IP.String()+" "+SeaHub)
		cmdAddRegistryHosts = fmt.Sprintf("%s && %s", cmdAddRegistryHosts, cmdAddSeaHubHosts)
	}
	joinCommands := []string{cmdAddRegistryHosts, certCMD, cmdAddHosts}
	if k.RegConfig.Username != "" && k.RegConfig.Password != "" {
		joinCommands = append(joinCommands, k.GenLoginCommand())
	}
	cmdUpdateHosts := fmt.Sprintf(RemoteUpdateEtcHosts, apiServerHost,
		getAPIServerHost(master, k.getAPIServerDomain()))

	return append(joinCommands, joinCmd, cmdUpdateHosts, RemoteCopyKubeConfig)
}

func (k *Runtime) sendKubeConfigFile(hosts []net.IP, kubeFile string) error {
	absKubeFile := fmt.Sprintf("%s/%s", clustercert.KubernetesConfigDir, kubeFile)
	sealerKubeFile := fmt.Sprintf("%s/%s", k.getBasePath(), kubeFile)
	return k.sendFileToHosts(hosts, sealerKubeFile, absKubeFile)
}

func (k *Runtime) sendNewCertAndKey(hosts []net.IP) error {
	return k.sendFileToHosts(hosts, k.getPKIPath(), clustercert.KubeDefaultCertPath)
}

func (k *Runtime) sendRegistryCertAndKey() error {
	return k.sendFileToHosts(k.cluster.GetMasterIPList()[:1], k.getCertsDir(), filepath.Join(k.getRootfs(), "certs"))
}

func (k *Runtime) sendRegistryCert(host []net.IP) error {
	cf := k.RegConfig
	err := k.sendFileToHosts(host, fmt.Sprintf("%s/%s.crt", k.getCertsDir(), cf.Domain), fmt.Sprintf("%s/%s:%s/%s.crt", DockerCertDir, cf.Domain, cf.Port, cf.Domain))
	if err != nil {
		return err
	}
	return k.sendFileToHosts(host, fmt.Sprintf("%s/%s.crt", k.getCertsDir(), cf.Domain), fmt.Sprintf("%s/%s:%s/%s.crt", DockerCertDir, SeaHub, cf.Port, cf.Domain))
}

func (k *Runtime) sendFileToHosts(Hosts []net.IP, src, dst string) error {
	eg, _ := errgroup.WithContext(context.Background())
	for _, node := range Hosts {
		node := node
		eg.Go(func() error {
			ssh, err := k.getHostSSHClient(node)
			if err != nil {
				return fmt.Errorf("failed to send file: %v", err)
			}
			if err := ssh.Copy(node, src, dst); err != nil {
				return fmt.Errorf("failed to send file: %v", err)
			}
			return err
		})
	}
	return eg.Wait()
}

func (k *Runtime) ReplaceKubeConfigV1991V1992(masters []net.IP) bool {
	// fix > 1.19.1 kube-controller-manager and kube-scheduler use the LocalAPIEndpoint instead of the ControlPlaneEndpoint.
	if k.getKubeVersion() == V1991 || k.getKubeVersion() == V1992 {
		for _, v := range masters {
			cmd := fmt.Sprintf(RemoteReplaceKubeConfig, KUBESCHEDULERCONFIGFILE, v, KUBECONTROLLERCONFIGFILE, v, KUBESCHEDULERCONFIGFILE)
			ssh, err := k.getHostSSHClient(v)
			if err != nil {
				logrus.Errorf("failed to replace kube config on %s: %v ", v, err)
				return false
			}
			if err := ssh.CmdAsync(v, cmd); err != nil {
				logrus.Errorf("failed to replace kube config on %s: %v ", v, err)
				return false
			}
		}
		return true
	}
	return false
}

func (k *Runtime) SendJoinMasterKubeConfigs(masters []net.IP, files ...string) error {
	for _, f := range files {
		if err := k.sendKubeConfigFile(masters, f); err != nil {
			return err
		}
	}
	if k.ReplaceKubeConfigV1991V1992(masters) {
		logrus.Info("set kubernetes v1.19.1 v1.19.2 kube config")
	}
	return nil
}

// joinMasterConfig is generated JoinCP nodes configuration by master ip.
func (k *Runtime) joinMasterConfig(masterIP net.IP) ([]byte, error) {
	k.Lock()
	defer k.Unlock()
	// TODO Using join file instead template
	k.setAPIServerEndpoint(fmt.Sprintf("%s:6443", k.cluster.GetMaster0IP()))
	k.setJoinAdvertiseAddress(masterIP)
	cGroupDriver, err := k.getCgroupDriverFromShell(masterIP)
	if err != nil {
		return nil, err
	}
	k.setCgroupDriver(cGroupDriver)
	return yaml.MarshalWithDelimiter(k.JoinConfiguration, k.KubeletConfiguration)
}

// sendJoinCPConfig send join CP nodes configuration
func (k *Runtime) sendJoinCPConfig(joinMaster []net.IP) error {
	k.Mutex = &sync.Mutex{}
	eg, _ := errgroup.WithContext(context.Background())
	for _, master := range joinMaster {
		ip := master
		eg.Go(func() error {
			joinConfig, err := k.joinMasterConfig(ip)
			if err != nil {
				return fmt.Errorf("failed to get join %s config: %v", ip, err)
			}
			cmd := fmt.Sprintf(RemoteJoinMasterConfig, joinConfig, k.getRootfs())
			ssh, err := k.getHostSSHClient(ip)
			if err != nil {
				return fmt.Errorf("failed to get ssh client of host(%s): %v", ip, err)
			}
			if err := ssh.CmdAsync(ip, cmd); err != nil {
				return fmt.Errorf("failed to set join kubeadm config on host(%s) with cmd(%s): %v", ip, cmd, err)
			}
			return nil
		})
	}
	return eg.Wait()
}

func (k *Runtime) CmdAsyncHosts(hosts []net.IP, cmd string) error {
	eg, _ := errgroup.WithContext(context.Background())
	for _, host := range hosts {
		ip := host
		eg.Go(func() error {
			ssh, err := k.getHostSSHClient(ip)
			if err != nil {
				logrus.Errorf("failed to exec command[%s] on host[%s]: %v", ip, cmd, err)
			}
			if err := ssh.CmdAsync(ip, cmd); err != nil {
				logrus.Errorf("failed to exec command[%s] on host[%s]: %v", ip, cmd, err)
			}
			return err
		})
	}
	return eg.Wait()
}

func vlogToStr(vlog int) string {
	str := strconv.Itoa(vlog)
	return " -v " + str
}

func (k *Runtime) Command(version string, name CommandType) (cmd string) {
	//cmds := make(map[CommandType]string)
	// Please convert your v1beta1 configuration files to v1beta2 using the
	// "kubeadm config migrate" command of kubeadm v1.15.x, so v1.14 not support multi network interface.
	cmds := map[CommandType]string{
		InitMaster: fmt.Sprintf(InitMaster115Lower, k.getRootfs()),
		JoinMaster: fmt.Sprintf(JoinMaster115Lower, k.cluster.GetMaster0IP(), k.getJoinToken(), k.getTokenCaCertHash(), k.getCertificateKey()),
		JoinNode:   fmt.Sprintf(JoinNode115Lower, k.getVIP(), k.getJoinToken(), k.getTokenCaCertHash()),
	}

	kv := versionUtils.Version(version)
	cmp, err := kv.Compare(V1150)
	//other version >= 1.15.x
	if err != nil {
		logrus.Errorf("failed to compare Kubernetes version: %s", err)
	}
	if cmp {
		cmds[InitMaster] = fmt.Sprintf(InitMaser115Upper, k.getRootfs())
		cmds[JoinMaster] = fmt.Sprintf(JoinMaster115Upper, k.getRootfs())
		cmds[JoinNode] = fmt.Sprintf(JoinNode115Upper, k.getRootfs())
	}

	v, ok := cmds[name]
	if !ok {
		logrus.Errorf("failed to get kubeadm command: %v", cmds)
		return ""
	}

	if runtime.IsInContainer() {
		return fmt.Sprintf("%s%s%s", v, vlogToStr(k.Vlog), " --ignore-preflight-errors=all")
	}
	if name == InitMaster || name == JoinMaster {
		return fmt.Sprintf("%s%s%s", v, vlogToStr(k.Vlog), " --ignore-preflight-errors=SystemVerification")
	}

	return fmt.Sprintf("%s%s", v, vlogToStr(k.Vlog))
}

func (k *Runtime) joinMasters(masters []net.IP) error {
	if len(masters) == 0 {
		return nil
	}
	// if its do not Load and Merge kubeadm config via init, need to redo it
	if err := k.MergeKubeadmConfig(); err != nil {
		return err
	}
	if err := k.WaitSSHReady(6, masters...); err != nil {
		return errors.Wrap(err, "join masters wait for ssh ready time out")
	}
	if err := k.GetJoinTokenHashAndKey(); err != nil {
		return err
	}
	if err := k.CopyStaticFiles(masters); err != nil {
		return err
	}
	if err := k.SendJoinMasterKubeConfigs(masters, AdminConf, ControllerConf, SchedulerConf); err != nil {
		return err
	}
	if err := k.sendRegistryCert(masters); err != nil {
		return err
	}
	// TODO only needs send ca?
	if err := k.sendNewCertAndKey(masters); err != nil {
		return err
	}
	if err := k.sendJoinCPConfig(masters); err != nil {
		return err
	}
	cmd := k.Command(k.getKubeVersion(), JoinMaster)
	// TODO for test skip dockerd dev version
	if cmd == "" {
		return fmt.Errorf("failed to get join master command, kubernetes version is %s", k.getKubeVersion())
	}

	for _, master := range masters {
		logrus.Infof("Start to join %s as master", master)

		hostname, err := k.getRemoteHostName(master)
		if err != nil {
			return err
		}
		cmds := k.JoinMasterCommands(master, cmd, hostname)
		client, err := k.getHostSSHClient(master)
		if err != nil {
			return err
		}

		if client.(*ssh.SSH).User != common.ROOT {
			cmds = append(cmds, RemoteNonRootCopyKubeConfig)
		}

		if err := client.CmdAsync(master, cmds...); err != nil {
			return fmt.Errorf("failed to exec command(%s) on master(%s): %v", cmds, master, err)
		}

		logrus.Infof("Succeeded in joining %s as master", master)
	}
	return nil
}

func (k *Runtime) deleteMasters(masters []net.IP) error {
	if len(masters) == 0 {
		return nil
	}
	eg, _ := errgroup.WithContext(context.Background())
	for _, master := range masters {
		master := master
		eg.Go(func() error {
			master := master
			logrus.Infof("Start to delete master %s", master)
			if err := k.deleteMaster(master); err != nil {
				logrus.Errorf("failed to delete master %s: %v", master, err)
			} else {
				logrus.Infof("Succeeded in deleting master %s", master)
			}
			return nil
		})
	}
	return eg.Wait()
}

func (k *Runtime) isHostName(master, host net.IP) (string, error) {
	hostString, err := k.CmdToString(master, "kubectl get nodes | grep -v NAME  | awk '{print $1}'", ",")
	if err != nil {
		return "", err
	}
	hostName, err := k.CmdToString(host, "hostname", "")
	if err != nil {
		return "", err
	}
	hosts := strings.Split(hostString, ",")
	var name string
	for _, h := range hosts {
		if strings.TrimSpace(h) == "" {
			continue
		} else {
			hh := strings.ToLower(h)
			fromH := strings.ToLower(hostName)
			if hh == fromH {
				name = h
				break
			}
		}
	}
	return name, nil
}

func (k *Runtime) deleteMaster(master net.IP) error {
	ssh, err := k.getHostSSHClient(master)
	if err != nil {
		return fmt.Errorf("failed to delete master: %v", err)
	}
	remoteCleanCmd := []string{fmt.Sprintf(RemoteCleanMasterOrNode, vlogToStr(k.Vlog)),
		fmt.Sprintf(RemoteRemoveAPIServerEtcHost, k.RegConfig.Domain),
		fmt.Sprintf(RemoteRemoveAPIServerEtcHost, SeaHub),
		fmt.Sprintf(RemoteRemoveRegistryCerts, k.RegConfig.Domain),
		fmt.Sprintf(RemoteRemoveRegistryCerts, SeaHub),
		fmt.Sprintf(RemoteRemoveAPIServerEtcHost, k.getAPIServerDomain())}

	//if the master to be removed is the execution machine, kubelet and ~./kube will not be removed and ApiServer host will be added.
	address, err := utilsnet.GetLocalHostAddresses()
	if err != nil || !utilsnet.IsLocalIP(master, address) {
		remoteCleanCmd = append(remoteCleanCmd, RemoveKubeConfig)
	} else {
		apiServerHost := getAPIServerHost(k.cluster.GetMaster0IP(), k.getAPIServerDomain())
		remoteCleanCmd = append(remoteCleanCmd,
			fmt.Sprintf(RemoteAddEtcHosts, apiServerHost, apiServerHost))
	}
	if err := ssh.CmdAsync(master, remoteCleanCmd...); err != nil {
		return err
	}

	// remove master
	masterIPs := []net.IP{}
	for _, ip := range k.cluster.GetMasterIPList() {
		if !ip.Equal(master) {
			masterIPs = append(masterIPs, ip)
		}
	}

	if len(masterIPs) > 0 {
		hostname, err := k.isHostName(k.cluster.GetMaster0IP(), master)
		if err != nil {
			return err
		}
		master0SSH, err := k.getHostSSHClient(k.cluster.GetMaster0IP())
		if err != nil {
			return fmt.Errorf("failed to get master0 ssh client: %v", err)
		}

		if err := master0SSH.CmdAsync(k.cluster.GetMaster0IP(), fmt.Sprintf(KubeDeleteNode, strings.TrimSpace(hostname))); err != nil {
			return fmt.Errorf("failed to delete node %s: %v", hostname, err)
		}
	}
	lvsImage := k.RegConfig.Repo() + "/fanux/lvscare:latest"
	yaml := ipvs.LvsStaticPodYaml(k.getVIP(), masterIPs, lvsImage)
	eg, _ := errgroup.WithContext(context.Background())
	for _, node := range k.cluster.GetNodeIPList() {
		node := node
		eg.Go(func() error {
			ssh, err := k.getHostSSHClient(node)
			if err != nil {
				logrus.Errorf("failed to update lvscare static pod on node(%s): %v", node, err)
			}
			if err := ssh.CmdAsync(node, RemoveLvscareStaticPod, fmt.Sprintf(CreateLvscareStaticPod, yaml)); err != nil {
				logrus.Errorf("failed to update lvscare static pod on node(%s): %v", node, err)
			}
			return err
		})
	}
	return eg.Wait()
}

func (k *Runtime) GetJoinTokenHashAndKey() error {
	cmd := fmt.Sprintf(`kubeadm init phase upload-certs --upload-certs -v %d`, k.Vlog)
	/*
		I0415 11:45:06.653868   14520 version.go:251] remote version is much newer: v1.21.0; falling back to: stable-1.16
		[upload-certs] Storing the certificates in Secret "kubeadm-certs" in the "kube-system" Namespace
		[upload-certs] Using certificate key:
		8376c70aaaf285b764b3c1a588740728aff493d7c2239684e84a7367c6a437cf
	*/
	output, err := k.CmdToString(k.cluster.GetMaster0IP(), cmd, "\r\n")
	if err != nil {
		return err
	}
	logrus.Debugf("[globals]decodeCertCmd: %s", output)
	slice := strings.Split(output, "Using certificate key:")
	if len(slice) != 2 {
		return fmt.Errorf("failed to get certifacate key: %s", slice)
	}
	key := strings.Replace(slice[1], "\r\n", "", -1)
	k.CertificateKey = strings.Replace(key, "\n", "", -1)
	cmd = fmt.Sprintf("kubeadm token create --print-join-command -v %d", k.Vlog)

	ssh, err := k.getHostSSHClient(k.cluster.GetMaster0IP())
	if err != nil {
		return fmt.Errorf("failed to get join token hash and key: %v", err)
	}
	out, err := ssh.Cmd(k.cluster.GetMaster0IP(), cmd)
	if err != nil {
		return fmt.Errorf("failed to create kubeadm join token: %v", err)
	}

	k.decodeMaster0Output(out)

	return nil
}

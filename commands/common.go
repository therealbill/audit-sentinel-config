package commands

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/therealbill/libredis/client"
	"github.com/therealbill/libredis/info"

	"strconv"
	"strings"
)

type ConfigIssue int

const (
	NOTENOUGHSENTINELS ConfigIssue = 1 << iota
	NOQUORUM
	NOSLAVES
	NOVALIDSLAVES
	HASINVALIDSENTINELS
)

func (ci ConfigIssue) String() string {
	s := ""
	switch ci {
	case NOTENOUGHSENTINELS:
		s += "Not Enough Sentinels"
	case NOQUORUM:
		s += "NO Quorum Possible"
	case NOSLAVES:
		s += "No Slaves Configured/Known"
	case NOVALIDSLAVES:
		s += "No Valid Slaves"
	case HASINVALIDSENTINELS:
		s += "Has Sentinels Configured which do not exist or are unreachable"
	}
	return s
}

var (
	lsconf         LocalSentinelConfig
	PodsWithIssues map[string][]string
)

type NodeInfo struct {
	Name      string
	Info      info.RedisInfoAll
	AuthToken string
}

func (n *NodeInfo) MaxMemory() (int64, error) {
	var config client.DialConfig
	config.Address = n.Name
	config.Password = n.AuthToken
	var maxmem int64
	conn, err := client.DialWithConfig(&config)
	if err != nil {
		log.Printf("Error on dial: Err='%s'", err)
		return maxmem, err
	}
	res, err := conn.ConfigGet("maxmemory")
	if err != nil {
		return maxmem, err
	}
	maxmem, err = strconv.ParseInt(res["maxmemory"], 10, 64)
	return maxmem, nil

}

type SentinelPodConfig struct {
	IP                 string
	Port               int
	Quorum             int
	Name               string
	AuthToken          string
	Sentinels          map[string]string
	ConfirmedSentinels map[string]string
	InvalidSentinels   map[string]string
}

type LocalSentinelConfig struct {
	Name               string
	Host               string
	Port               int
	ManagedPodConfigs  map[string]SentinelPodConfig
	Dir                string
	KnownSentinels     map[string]string
	InvalidSentinels   map[string]string
	ConfigIssueMapping map[ConfigIssue][]SentinelPodConfig
}

func LoadSentinelConfigFile() error {
	lsconf.ManagedPodConfigs = make(map[string]SentinelPodConfig)
	lsconf.KnownSentinels = make(map[string]string)
	file, err := os.Open("/etc/redis/sentinel.conf")
	defer file.Close()
	if err != nil {
		log.Print(err)
		return err
	}
	bf := bufio.NewReader(file)
	for {
		rawline, err := bf.ReadString('\n')
		if err == nil || err == io.EOF {
			line := strings.TrimSpace(rawline)
			// ignore comments
			if strings.Contains(line, "#") {
				continue
			}
			entries := strings.Split(line, " ")
			//Most values are key/value pairs
			switch entries[0] {
			case "sentinel": // Have a sentinel directive
				err := extractSentinelDirective(entries[1:])
				if err != nil {
					// TODO: Fix this to return a different error if we can't
					// connect to the sentinel
					log.Printf("Misshapen sentinel directive: '%s'", line, err)
				}
			case "port":
				iport, _ := strconv.Atoi(entries[1])
				lsconf.Port = iport
				if lsconf.Host > "" {
					lsconf.Name = fmt.Sprintf("%s:%d", lsconf.Host, lsconf.Port)
				}
			case "dir":
				lsconf.Dir = entries[1]
			case "bind":
				lsconf.Host = entries[1]
				log.Printf("Local sentinel is listening on IP %s", lsconf.Host)
				if lsconf.Port > 0 {
					lsconf.Name = fmt.Sprintf("%s:%d", lsconf.Host, lsconf.Port)
				}
			case "":
				if err == io.EOF {
					log.Print("File load complete?")
					return nil
				}
			// ignore these
			case "maxclients":
			default:
				log.Printf("Unhandled Config Directive: %s", line)
			}
		} else {
			log.Print("=============== LOAD FILE ERROR ===============")
			log.Fatal(err)
		}
	}
	lsconf.Name = fmt.Sprintf("%s:%d", lsconf.Host, lsconf.Port)
	return nil
}

func extractSentinelDirective(entries []string) error {
	switch entries[0] {
	case "monitor":
		pname := entries[1]
		port, _ := strconv.Atoi(entries[3])
		quorum, _ := strconv.Atoi(entries[4])
		spc := SentinelPodConfig{Name: pname, IP: entries[2], Port: port, Quorum: quorum}
		spc.Sentinels = make(map[string]string)
		// normally we should not see duplicate IP:PORT combos, so detect them
		// and ignore the second one if found, reporting the error condition
		// this will require tracking ip:port pairs...
		addr := fmt.Sprintf("%s:%d", entries[2], port)
		_, exists := lsconf.ManagedPodConfigs[addr]
		if !exists {
			lsconf.ManagedPodConfigs[entries[1]] = spc
		}
		return nil

	case "auth-pass":
		pname := entries[1]
		pc := lsconf.ManagedPodConfigs[pname]
		pc.AuthToken = entries[2]
		lsconf.ManagedPodConfigs[pname] = pc
		return nil

	case "known-sentinel":
		podname := entries[1]
		sentinel_address := entries[2] + ":" + entries[3]
		pc := lsconf.ManagedPodConfigs[podname]
		pc.Sentinels[sentinel_address] = ""
		isMe := sentinel_address == lsconf.Name
		if !isMe {
			lsconf.KnownSentinels[sentinel_address] = sentinel_address
		}
		return nil

	case "known-slave":
		// Currently ignoring this, but may add call to a node manager.
		return nil

	case "config-epoch", "leader-epoch", "current-epoch", "down-after-milliseconds", "maxclients":
		// We don't use these keys
		return nil

	default:
		err := fmt.Errorf("Unhandled sentinel directive: '%+v'", entries)
		log.Print(err)
		return nil
	}
	return nil
}

func sentinelAvailable(name string) (bool, error) {
	if lsconf.InvalidSentinels == nil {
		lsconf.InvalidSentinels = make(map[string]string)
	}
	_, knownInvalid := lsconf.InvalidSentinels[name]
	if knownInvalid {
		return false, fmt.Errorf("Known Invalid Sentinel")
	}
	conn, err := client.DialWithConfig(&client.DialConfig{Address: name, Timeout: 2 * time.Second})
	if err != nil {
		lsconf.InvalidSentinels[name] = ""
		return false, err
	}
	err = conn.Ping()
	return true, err
}

func (pc *SentinelPodConfig) ConfigIssues() (issues []ConfigIssue) {
	if len(pc.InvalidSentinels) > 0 {
		issues = append(issues, HASINVALIDSENTINELS)
	}
	if len(pc.ConfirmedSentinels) < pc.Quorum {
		issues = append(issues, NOQUORUM)
	}
	if len(pc.ConfirmedSentinels) < pc.Quorum {
		issues = append(issues, NOTENOUGHSENTINELS)
	}
	//log.Printf("Config: %+v", pc)
	/// TODO: Add Slave Tests

	// set errorcode
	return
}

func (pc *SentinelPodConfig) validatePodSentinels() {
	if pc.ConfirmedSentinels == nil {
		pc.ConfirmedSentinels = make(map[string]string)
	}
	if pc.InvalidSentinels == nil {
		pc.InvalidSentinels = make(map[string]string)
	}
	for sentinel, _ := range pc.Sentinels {
		_, knownInvalid := lsconf.InvalidSentinels[sentinel]
		if knownInvalid {
			pc.InvalidSentinels[sentinel] = ""
		}
		isValid, _ := sentinelAvailable(sentinel)
		if isValid {
			pc.ConfirmedSentinels[sentinel] = ""
		} else {
			pc.InvalidSentinels[sentinel] = ""
			//log.Printf("%s err:%s", sentinel, err)
		}
	}
}

func main() {
	log.Print("Running sentinel config audit")
	LoadSentinelConfigFile()
	fmt.Printf("Configuration Audit Run for Sentinel '%s' at %s\n", lsconf.Name, time.Now())
	lsconf.ConfigIssueMapping = make(map[ConfigIssue][]SentinelPodConfig)
	if lsconf.Name == "" {
		log.Print("WARNING: MISSING BIND DIRECTIVE!")
		fmt.Println("Bind Statement Present: False")
	} else {
		fmt.Println("Bind Statement Present: True")
	}
	fmt.Println()
	fmt.Printf("Known Sentinels (%d):\n", len(lsconf.KnownSentinels))
	fmt.Printf("=====================\n")
	for s, _ := range lsconf.KnownSentinels {
		isAvailable, err := sentinelAvailable(s)
		if isAvailable {
			fmt.Printf("%s (Available)\n", s)
		} else {
			fmt.Printf("%s (MISSING - err: '%s')\n", s, err)
		}
	}
	fmt.Println()

	fmt.Printf("Locally Configured Pods: %d\n", len(lsconf.ManagedPodConfigs))
	PodsWithIssues := 0
	for k, v := range lsconf.ManagedPodConfigs {
		//log.Printf("%s: %+v", k, v)
		v.validatePodSentinels()
		issues := v.ConfigIssues()
		if len(issues) > 0 {
			log.Printf("%s has %d configuration issues", k, len(issues))
			PodsWithIssues++
			for _, issue := range issues {
				lsconf.ConfigIssueMapping[issue] = append(lsconf.ConfigIssueMapping[issue], v)
			}
		}
	}

	fmt.Printf("%d of %d Pods have configuration issues", PodsWithIssues, len(lsconf.ManagedPodConfigs))
	fmt.Println()
	if PodsWithIssues > 0 {
		for issue, podlist := range lsconf.ConfigIssueMapping {
			fmt.Printf("\nConfig Issue: '%s'\n", issue)
			fmt.Printf("Pods with issue %d\n", len(podlist))
			fmt.Println("=============================")
			for _, pod := range podlist {
				fmt.Printf("  %s\n", pod.Name)
			}

		}
	}

}

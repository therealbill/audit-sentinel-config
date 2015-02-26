package main

import (
	"bufio"
	"flag"
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
	DUPLICATEMASTERIP
	DUPLICATESLAVEIP
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
	case DUPLICATEMASTERIP:
		s += "Shares a master IP with another pod."
	case DUPLICATESLAVEIP:
		s += "Shares a slave IP with another pod."
	}
	return s
}

var (
	lsconf               LocalSentinelConfig
	PodsWithIssues       map[string][]string
	masterIPtoPodMapping map[string]SentinelPodConfig
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
	Slaves             []string
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
		podname := entries[1]
		slave := entries[2] + ":" + entries[3]
		pc := lsconf.ManagedPodConfigs[podname]
		pc.Slaves = append(pc.Slaves, slave)
		lsconf.ManagedPodConfigs[podname] = pc
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

type Report []string

func (r *Report) String() string {
	return fmt.Sprint(*r)
}

func (r *Report) Set(value string) error {
	for _, rpt := range strings.Split(value, ",") {
		*r = append(*r, rpt)
	}
	return nil
}

var reportFlag Report
var showByError bool

func init() {
	flag.Var(&reportFlag, "report", "comma-separated list of reports to run")
	flag.BoolVar(&showByError, "byerror", false, "For each found error show all pods which have it")
}

func BaseConfigReport() {
	if lsconf.Name == "" {
		log.Print("WARNING: MISSING BIND DIRECTIVE!")
		fmt.Println("Bind Statement Present: False")
	} else {
		fmt.Println("Bind Statement Present: True")
	}
	if lsconf.Port != 26379 {
		fmt.Printf("WARNING: Sentinel is running on non-standard port: %d.", lsconf.Port)
	}

}

func KnownSentinelsReport() {
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
}

func PodReport() {
	fmt.Printf("%d of %d Pods have configuration issues", PodsWithIssues, len(lsconf.ManagedPodConfigs))
	fmt.Println()
	fmt.Printf("Locally Configured Pods: %d\n", len(lsconf.ManagedPodConfigs))
	PodsWithIssues := 0
	for k, v := range lsconf.ManagedPodConfigs {
		//log.Printf("%s: %+v", k, v)
		v.validatePodSentinels()
		issues := v.ConfigIssues()
		if len(issues) > 0 {
			fmt.Printf("%s has %d configuration issues", k, len(issues))
			PodsWithIssues++
			for _, issue := range issues {
				lsconf.ConfigIssueMapping[issue] = append(lsconf.ConfigIssueMapping[issue], v)
			}
		}
	}
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

func FindDupeMasterIPs() {
	log.Print("Looking for duplicated master IPs")
	masterIPtoPodMapping = make(map[string]SentinelPodConfig)
	for _, v := range lsconf.ManagedPodConfigs {
		opod, dupe := masterIPtoPodMapping[v.IP]
		if dupe {
			fmt.Printf("Found Duplicate master! %s and %s share master IP %s", opod.Name, v.Name, v.IP)
			lsconf.ConfigIssueMapping[DUPLICATEMASTERIP] = append(lsconf.ConfigIssueMapping[DUPLICATEMASTERIP], v)
			lsconf.ConfigIssueMapping[DUPLICATEMASTERIP] = append(lsconf.ConfigIssueMapping[DUPLICATEMASTERIP], opod)
			// test v
			_, err := client.DialWithConfig(&client.DialConfig{Address: fmt.Sprintf("%s:%d", v.IP, v.Port), Password: v.AuthToken})
			if err != nil {
				log.Printf("Pod %s could not auth to %s, recommend deleting this one.", v.Name, v.IP)
			} else {
				// test opod
				_, err := client.DialWithConfig(&client.DialConfig{Address: fmt.Sprintf("%s:%d", opod.IP, opod.Port), Password: opod.AuthToken})
				if err != nil {
					log.Printf("Pod %s could not auth to %s, recommend deleting this one.", opod.Name, opod.IP)
				}
			}

		} else {
			masterIPtoPodMapping[v.IP] = v
		}
	}

}

func FindDupeSlaveIPs() {
	log.Print("Looking for duplicated slave IPs")
	slaveIPtoPodMapping := make(map[string]SentinelPodConfig)
	for _, v := range lsconf.ManagedPodConfigs {
		for _, slave := range v.Slaves {
			if opod, dupe := slaveIPtoPodMapping[slave]; dupe {
				log.Printf("Found Duplicate slave! %s and %s share slave IP %s", opod.Name, v.Name, slave)
				lsconf.ConfigIssueMapping[DUPLICATESLAVEIP] = append(lsconf.ConfigIssueMapping[DUPLICATESLAVEIP], v)
				lsconf.ConfigIssueMapping[DUPLICATESLAVEIP] = append(lsconf.ConfigIssueMapping[DUPLICATESLAVEIP], opod)
			} else {
				slaveIPtoPodMapping[slave] = v
			}
			if opod, dupe := masterIPtoPodMapping[slave]; dupe {
				log.Printf("Found Duplicate slave/master! %s is master for %s and slave for %s", slave, opod.Name, v.Name)
				lsconf.ConfigIssueMapping[DUPLICATESLAVEIP] = append(lsconf.ConfigIssueMapping[DUPLICATESLAVEIP], v)
				lsconf.ConfigIssueMapping[DUPLICATESLAVEIP] = append(lsconf.ConfigIssueMapping[DUPLICATESLAVEIP], opod)
			} else {
				slaveIPtoPodMapping[slave] = v
			}
		}
	}

}

func main() {
	flag.Parse()
	log.Printf("Reports to run: %+v", reportFlag)
	log.Print("Running sentinel config audit")
	lsconf.ConfigIssueMapping = make(map[ConfigIssue][]SentinelPodConfig)
	LoadSentinelConfigFile()
	fmt.Printf("Configuration Audit Run for Sentinel '%s' at %s\n", lsconf.Name, time.Now())
	fmt.Println()

	for _, rep := range reportFlag {
		switch rep {
		case "baseconfig":
			BaseConfigReport()

		case "known-sentinels":
			KnownSentinelsReport()

		case "all":
			BaseConfigReport()
			KnownSentinelsReport()
			FindDupeMasterIPs()
			FindDupeSlaveIPs()
			PodReport()

		default:
			fmt.Printf("Unknown report '%s'\n", rep)

		}
	}
}

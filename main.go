package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ReneKroon/ttlcache"
	"github.com/miekg/dns"
)

type arrayFlags []string

func (af *arrayFlags) String() string {
	ret := ""
	for _, v := range *af {
		if ret != "" {
			ret += "\n"

		}
		ret += v
	}
	return ret
}
func (af *arrayFlags) Set(value string) error {
	*af = append(*af, value)
	return nil
}

type serverConfig struct {
	host    string
	domain  string
	timeout time.Duration
}

type dnsHandler struct {
	servers []serverConfig
	cache   *ttlcache.Cache
	ttl     time.Duration
}
type orderedResult struct {
	order   int
	answers []dns.RR
	server  string
}

func resolve(server string, timeout time.Duration, domain string, qtype uint16) []dns.RR {
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(domain), qtype)
	m.RecursionDesired = true

	c := new(dns.Client)
	c.DialTimeout = timeout
	c.ReadTimeout = timeout
	c.WriteTimeout = timeout
	in, _, err := c.Exchange(m, server)
	if err != nil {
		fmt.Println(err)
		return make([]dns.RR, 0)
	}
	return in.Answer
}

func (h *dnsHandler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	msg := new(dns.Msg)
	msg.SetReply(r)
	msg.Authoritative = true
	jobs := len(h.servers)
	ch := make(chan *orderedResult, jobs)
	for i, cfg := range h.servers {
		go func(order int, msg *dns.Msg, cfg serverConfig) {
			answers := make([]dns.RR, 0)
			for _, question := range r.Question {
				ds_name := question.Name
				if strings.Count(question.Name, ".") == 1 {
					if cfg.domain == "" {
						continue
					} else {
						ds_name = question.Name + cfg.domain
					}
				}
				// if val, ok := h.cache.Get(ds_name); ok {
				// 	answers = append(answers, val.([]dns.RR)...)
				// 	continue
				// }
				// fmt.Printf("Received query: %s\n", ds_name)
				ans := resolve(cfg.host, cfg.timeout/2, ds_name, question.Qtype)
				answers = append(answers, ans...)
				// if len(ans) > 0 {
				// 	h.cache.SetWithTTL(ds_name, ans, h.ttl)
				// }
			}
			ch <- &orderedResult{order: order, answers: answers, server: cfg.host}
		}(i, msg, cfg)
	}
	results := make([]*orderedResult, 0)
	for i := 0; i < jobs; i++ {
		r := <-ch
		results = append(results, r)
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].order < results[j].order
	})
	for _, r := range results {
		if len(r.answers) > 0 {
			msg.Answer = r.answers
			break
		}
	}
	w.WriteMsg(msg)
}

func main() {
	var serverFlags arrayFlags
	flag.Var(&serverFlags, "dns", "dns servers to use, in format of <ip>[:port][@search domain]")
	listen := flag.String("listen", ":5300", "address to listen, eg: 127.0.0.1:5300")
	timeout := flag.Uint("timeout", 4, "timeout value in time.Seconds")
	ttl := flag.Uint("ttl", 600, "cache TTL in time.Seconds")
	flag.Parse()
	servers := make([]serverConfig, 0)
	for _, addr := range serverFlags {
		host := ""
		port := 53
		search := ""
		pos := strings.Index(addr, "@")
		if pos > 1 {
			host = addr[:pos]
			search = addr[pos+1:]
		} else {
			host = addr
		}
		pos = strings.LastIndex(host, ":")
		if pos > 0 {
			port, _ = strconv.Atoi(host[pos+1:])
			host = host[:pos]
		}
		if host == "" {
			os.Stderr.WriteString(fmt.Sprintf("Invalid address %s, skip this value", addr))
			continue
		}
		sc := serverConfig{}
		sc.timeout = time.Duration(*timeout) * time.Second
		sc.host = fmt.Sprintf("%s:%d", host, port)
		if len(search) > 1 && search[len(search)-1] != '.' {
			search += "."
		}
		sc.domain = search
		servers = append(servers, sc)
	}
	if len(servers) == 0 {
		sc := serverConfig{}
		sc.host = "127.0.0.1:53"
		sc.domain = ""
		sc.timeout = time.Duration(*timeout) * time.Second
		servers = append(servers, sc)
	}
	fmt.Printf("Valid server config: %s\n", servers)

	handler := dnsHandler{
		cache: ttlcache.NewCache(),
		ttl:   time.Duration(*ttl) * time.Second,
	}
	handler.servers = servers
	server := &dns.Server{
		Addr:         *listen,
		Net:          "udp",
		Handler:      &handler,
		UDPSize:      65535,
		ReusePort:    true,
		ReadTimeout:  time.Duration(*timeout) * time.Second,
		WriteTimeout: time.Duration(*timeout) * time.Second,
		// DisableBackground: true,
	}

	fmt.Println("Starting DNS server on port 53")
	err := server.ListenAndServe()
	if err != nil {
		fmt.Printf("Failed to start server: %s\n", err.Error())
	}
}

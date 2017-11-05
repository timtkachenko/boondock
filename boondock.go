package main

import (
	"encoding/json"
	"github.com/pipedrive/tanker/diplomat"
	"github.com/pipedrive/tanker/logger"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
)

type (
	Route struct {
		protocol string
		host     string
		path     string
		service  string
		auth     bool
	}
	routeService struct {
		Auth    bool   `json:"auth"`
		Service string `json:"service"`
	}
	node struct {
		children     []*node
		component    string
		isNamedParam bool
		route        *Route
		//methods      map[string]Handle
	}
)

func (n *node) addNode(path string, route *Route) {
	components := strings.Split(path, "/")[1:]
	count := len(components)

	for {
		aNode, component := n.traverse(components, nil)
		if aNode.component == component && count == 1 { // update an existing node.
			aNode.route = route
			return
		}
		newNode := node{component: component, isNamedParam: false}

		if len(component) > 0 && component[0] == ':' { // check if it is a named param.
			newNode.isNamedParam = true
		}
		if count == 1 { // this is the last component of the url resource, so it gets the handler.
			newNode.route = route
		}
		aNode.children = append(aNode.children, &newNode)
		count--
		if count == 0 {
			break
		}
	}
}
func (n *node) traverse(components []string, params url.Values) (*node, string) {
	component := components[0]
	if len(n.children) > 0 { // no children, then bail out.
		for _, child := range n.children {
			if component == child.component || child.isNamedParam {
				if child.isNamedParam && params != nil {
					params.Add(child.component[1:], component)
				}
				next := components[1:]
				if len(next) > 0 { // http://xkcd.com/1270/
					return child.traverse(next, params) // tail recursion is it's own reward.
				} else {
					return child, component
				}
			}
			if strings.HasSuffix(child.component, "*") && strings.HasPrefix(component, strings.TrimRight(child.component, "*")) {
				logger.Log("* next %v", component)
				return child, component
			}
			if child.component == "*" {
				logger.Log("* next %v", components[1:])
				//return child, component
			}
		}
	}
	return n, component
}

func makeRoute(key, val string) *Route {
	parts := strings.Split(key, "/")
	path := strings.Split(parts[3], "|")
	data := routeService{}
	err := json.Unmarshal([]byte(val), &data)
	if err != nil {
		logger.Error(err)
	}
	return &Route{
		parts[1],
		parts[2],
		"/" + strings.Join(path, "/"),
		data.Service,
		data.Auth,
	}
}

var (
	schemeList map[string]map[string]*node
)

func makeList() {
	routeListRaw, err := diplomat.Instance().List("route/")
	if !err {
		logger.Error("can't get routes")
		return
	}
	schemeList = map[string]map[string]*node{}
	for _, item := range routeListRaw {
		//routesList = append(routesList, makeRoute(item.Key, string(item.Value)))
		route := makeRoute(item.Key, string(item.Value))
		logger.Log("r: %v", route)
		if schemeList[route.protocol] == nil {
			schemeList[route.protocol] = map[string]*node{}
		}
		if schemeList[route.protocol][route.host] == nil {
			schemeList[route.protocol][route.host] = &node{component: "/", isNamedParam: false}
		}
		schemeList[route.protocol][route.host].addNode(route.path, route)
	}
}

var wg sync.WaitGroup

func main() {
	makeList()
	target := url.URL{
		Path:   "/",
		Host:   "app.pipedrive.in",
		Scheme: "http",
	}
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		targetQuery := target.RawQuery
		director := func(req *http.Request) {
			found := matchHost("https", req.Host)
			if found != nil && req.URL.Path != "/" {
				params := req.Form
				parts := strings.Split(req.URL.Path, "/")
				result, key := found.traverse(parts[1:], params)

				var route *Route
				if result == nil {
					return
				}

				route = result.route
				logger.Log("Route %s : %v", key, route)
				if route == nil {
					route = &Route{
						service: "webapp",
					}
				}
				service, _ := diplomat.Instance().GetServiceInstance(route.service)
				logger.Log("service %s : %v, %s", service.Name, service, service.MakeAddressFormatted())
				req.URL.Scheme = "http"
				req.URL.Host = service.MakeAddressFormatted()
				req.Header.Add("Host", r.Host)
			} else {
				req.URL.Scheme = target.Scheme
				req.URL.Host = target.Host
			}
			//req.URL.Path = singleJoiningSlash(target.Path, req.URL.Path)
			if targetQuery == "" || req.URL.RawQuery == "" {
				req.URL.RawQuery = targetQuery + req.URL.RawQuery
			} else {
				req.URL.RawQuery = targetQuery + "&" + req.URL.RawQuery
			}
		}

		proxy := &httputil.ReverseProxy{Director: director}
		proxy.ServeHTTP(w, r)
	})
	log.Fatal(http.ListenAndServe(":3000", nil))
}

func matchHost(schemeIn string, hostIn string) *node {
	routeList := schemeList[schemeIn]
	for host, node := range routeList {
		if host == hostIn {
			return node
		}
		if strings.HasPrefix(host, "*") && strings.HasSuffix(hostIn, strings.TrimLeft(host, "*")) {
			return node
		}
	}
	return nil
}

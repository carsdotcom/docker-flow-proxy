package main

import (
	"./actions"
	"./proxy"
	"./server"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"io/ioutil"
	"bytes"
)

const (
	DISTRIBUTED = "Distributed to all instances"
)

type Server interface {
	Execute(args []string) error
	ServeHTTP(w http.ResponseWriter, req *http.Request)
}

type Serve struct {
	IP              string `short:"i" long:"ip" default:"0.0.0.0" env:"IP" description:"IP the server listens to."`
	Mode            string `short:"m" long:"mode" env:"MODE" description:"If set to 'swarm', proxy will operate assuming that Docker service from v1.12+ is used."`
	ListenerAddress string `short:"l" long:"listener-address" env:"LISTENER_ADDRESS" description:"The address of the Docker Flow: Swarm Listener. The address matches the name of the Swarm service (e.g. swarm-listener)"`
	Port            string `short:"p" long:"port" default:"8080" env:"PORT" description:"Port the server listens to."`
	ServiceName     string `short:"n" long:"service-name" default:"proxy" env:"SERVICE_NAME" description:"The name of the proxy service. It is used only when running in 'swarm' mode and must match the '--name' parameter used to launch the service."`
	actions.BaseReconfigure
}

var serverImpl = Serve{}
var cert server.Certer = server.NewCert("/certs")

type SwarmService struct {
	Name string `json:"name,omitempty"`
	Path string `json:"path,omitempty"`
}

type Response struct {
	Status               string
	Message              string
	ServiceName          string
	AclName              string
	ServiceColor         string
	ServicePath          []string
	ServiceDomain        []string
	ServiceCert          string
	OutboundHostname     string
	ConsulTemplateFePath string
	ConsulTemplateBePath string
	PathType             string
	SkipCheck            bool
	Mode                 string
	Port                 string
	HttpsPort            int
	Distribute           bool
	Users                []actions.User
	ReqRepSearch         string
	ReqRepReplace        string
	TemplateFePath       string
	TemplateBePath       string
}

func (m *Serve) Execute(args []string) error {
	// TODO: Change map[string]bool{} env vars
	if proxy.Instance == nil {
		proxy.Instance = proxy.NewHaProxy(m.TemplatesPath, m.ConfigsPath, map[string]bool{})
	}
	logPrintf("Starting HAProxy")
	m.setConsulAddresses()
	NewRun().Execute([]string{})
	address := fmt.Sprintf("%s:%s", m.IP, m.Port)
	recon := actions.NewReconfigure(m.BaseReconfigure, actions.ServiceReconfigure{})
	lAddr := ""
	if len(m.ListenerAddress) > 0 {
		lAddr = fmt.Sprintf("http://%s:8080", m.ListenerAddress)
	}
	cert.Init()
	if err := recon.ReloadAllServices(
		m.ConsulAddresses,
		m.InstanceName,
		m.Mode,
		lAddr,
	); err != nil {
		return err
	}
	logPrintf(`Starting "Docker Flow: Proxy"`)
	if err := httpListenAndServe(address, m); err != nil {
		return err
	}
	return nil
}

func (m *Serve) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if !strings.EqualFold(req.URL.Path, "/v1/test") {
		logPrintf("Processing request %s", req.URL)
	}
	switch req.URL.Path {
	case "/v1/docker-flow-proxy/services":
		m.services(w, req)
	case "/v1/docker-flow-proxy/reconfigure":
		m.reconfigure(w, req)
	case "/v1/docker-flow-proxy/remove":
		m.remove(w, req)
	case "/v1/docker-flow-proxy/config":
		m.config(w, req)
	case "/v1/docker-flow-proxy/cert":
		if req.Method == "PUT" {
			cert.Put(w, req)
		} else {
			logPrintf("/v1/docker-flow-proxy/cert endpoint allows only PUT requests. Your was %s", req.Method)
			w.WriteHeader(http.StatusNotFound)
		}
	case "/v1/docker-flow-proxy/certs":
		cert.GetAll(w, req)
	case "/v1/test", "/v2/test":
		js, _ := json.Marshal(Response{Status: "OK"})
		httpWriterSetContentType(w, "application/json")
		if !strings.EqualFold(req.URL.Path, "/v1/test") {

		}
		w.WriteHeader(http.StatusOK)
		w.Write(js)
	default:
		logPrintf("The endpoint %s is not supported", req.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

func (m *Serve) isValidReconf(name string, path, domain []string, templateFePath string) bool {
	return len(name) > 0 && (len(path) > 0 || len(templateFePath) > 0)
}

func (m *Serve) services(w http.ResponseWriter, req *http.Request) {
	httpWriterSetContentType(w, "text/html")
  lAddr := ""
	if len(m.ListenerAddress) > 0 {
		lAddr = fmt.Sprintf("http://%s:8080/v1/docker-flow-swarm-listener/services", m.ListenerAddress)
	} 
	resp, err := http.Get(lAddr)
	defer resp.Body.Close()
	if err != nil {
		logPrintf("Unable to get registered services")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`<h3>Unable to get registered services</h3>`))
	} else {
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			logPrintf("Cannot read response body")
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`<h3>Cannot read response body</h3>`))
		} else {
			var services []SwarmService
			err := json.Unmarshal(body, &services)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`<h3>Unable to unmarshal json</h3>`))
			} else {
				w.WriteHeader(http.StatusOK)
				var buffer bytes.Buffer
				if len(services) > 0 {
					buffer.WriteString("<h2>Services deployed in this cluster</h2></p>")
					buffer.WriteString("<ul>")
					for _, service := range services {
						li := fmt.Sprintf("<li>%s --> %s</li>", service.Name, service.Path)
						buffer.WriteString(li)
					}
					buffer.WriteString("</ul>")
				} else {
					buffer.WriteString("<h2>There are no services deployed in this cluster</h2></p>")
				}
				w.Write(buffer.Bytes())
			}
		}
	}
}

func (m *Serve) reconfigure(w http.ResponseWriter, req *http.Request) {
	sr := actions.ServiceReconfigure{
		ServiceName:          req.URL.Query().Get("serviceName"),
		AclName:              req.URL.Query().Get("aclName"),
		ServiceColor:         req.URL.Query().Get("serviceColor"),
		ServiceCert:          req.URL.Query().Get("serviceCert"),
		OutboundHostname:     req.URL.Query().Get("outboundHostname"),
		ConsulTemplateFePath: req.URL.Query().Get("consulTemplateFePath"),
		ConsulTemplateBePath: req.URL.Query().Get("consulTemplateBePath"),
		PathType:             req.URL.Query().Get("pathType"),
		Port:                 req.URL.Query().Get("port"),
		Mode:                 m.Mode,
		ReqRepSearch:         req.URL.Query().Get("reqRepSearch"),
		ReqRepReplace:        req.URL.Query().Get("reqRepReplace"),
		TemplateFePath:       req.URL.Query().Get("templateFePath"),
		TemplateBePath:       req.URL.Query().Get("templateBePath"),
	}
	if len(req.URL.Query().Get("httpsPort")) > 0 {
		sr.HttpsPort, _ = strconv.Atoi(req.URL.Query().Get("httpsPort"))
	}
	if len(req.URL.Query().Get("servicePath")) > 0 {
		sr.ServicePath = strings.Split(req.URL.Query().Get("servicePath"), ",")
	}
	if len(req.URL.Query().Get("serviceDomain")) > 0 {
		sr.ServiceDomain = strings.Split(req.URL.Query().Get("serviceDomain"), ",")
	}
	if len(req.URL.Query().Get("skipCheck")) > 0 {
		sr.SkipCheck, _ = strconv.ParseBool(req.URL.Query().Get("skipCheck"))
	}
	if len(req.URL.Query().Get("distribute")) > 0 {
		sr.Distribute, _ = strconv.ParseBool(req.URL.Query().Get("distribute"))
	}
	if len(req.URL.Query().Get("users")) > 0 {
		users := strings.Split(req.URL.Query().Get("users"), ",")
		for _, user := range users {
			userPass := strings.Split(user, ":")
			sr.Users = append(sr.Users, actions.User{Username: userPass[0], Password: userPass[1]})
		}
	}
	response := Response{
		Status:               "OK",
		ServiceName:          sr.ServiceName,
		AclName:              sr.AclName,
		ServiceColor:         sr.ServiceColor,
		ServicePath:          sr.ServicePath,
		ServiceDomain:        sr.ServiceDomain,
		ServiceCert:          sr.ServiceCert,
		OutboundHostname:     sr.OutboundHostname,
		ConsulTemplateFePath: sr.ConsulTemplateFePath,
		ConsulTemplateBePath: sr.ConsulTemplateBePath,
		PathType:             sr.PathType,
		SkipCheck:            sr.SkipCheck,
		Mode:                 sr.Mode,
		Port:                 sr.Port,
		HttpsPort:		      sr.HttpsPort,
		Distribute:           sr.Distribute,
		Users:                sr.Users,
		ReqRepSearch:         sr.ReqRepSearch,
		ReqRepReplace:        sr.ReqRepReplace,
		TemplateFePath:       sr.TemplateFePath,
		TemplateBePath:       sr.TemplateBePath,
	}
	if m.isValidReconf(sr.ServiceName, sr.ServicePath, sr.ServiceDomain, sr.ConsulTemplateFePath) {
		if (strings.EqualFold("service", m.Mode) || strings.EqualFold("swarm", m.Mode)) && len(sr.Port) == 0 {
			m.writeBadRequest(w, &response, `When MODE is set to "service" or "swarm", the port query is mandatory`)
		} else if sr.Distribute {
			srv := server.Serve{}
			if status, err := srv.SendDistributeRequests(req, m.Port, m.ServiceName); err != nil || status >= 300 {
				m.writeInternalServerError(w, &response, err.Error())
			} else {
				response.Message = DISTRIBUTED
				w.WriteHeader(http.StatusOK)
			}
		} else {
			if len(sr.ServiceCert) > 0 {
				// Replace \n with proper carriage return as new lines are not supported in labels
				sr.ServiceCert = strings.Replace(sr.ServiceCert, "\\n", "\n", -1)
				if len(sr.ServiceDomain) > 0 {
					cert.PutCert(sr.ServiceDomain[0], []byte(sr.ServiceCert))
				} else {
					cert.PutCert(sr.ServiceName, []byte(sr.ServiceCert))
				}
			}
			action := actions.NewReconfigure(m.BaseReconfigure, sr)
			if err := action.Execute([]string{}); err != nil {
				m.writeInternalServerError(w, &response, err.Error())
			} else {
				w.WriteHeader(http.StatusOK)
			}
		}
	} else {
		m.writeBadRequest(w, &response, "The following queries are mandatory: (serviceName and servicePath) or (serviceName, consulTemplateFePath, and consulTemplateBePath)")
	}
	httpWriterSetContentType(w, "application/json")
	js, _ := json.Marshal(response)
	w.Write(js)
}

func (m *Serve) writeBadRequest(w http.ResponseWriter, resp *Response, msg string) {
	resp.Status = "NOK"
	resp.Message = msg
	w.WriteHeader(http.StatusBadRequest)
}

func (m *Serve) writeInternalServerError(w http.ResponseWriter, resp *Response, msg string) {
	resp.Status = "NOK"
	resp.Message = msg
	w.WriteHeader(http.StatusInternalServerError)
}

func (m *Serve) remove(w http.ResponseWriter, req *http.Request) {
	serviceName := req.URL.Query().Get("serviceName")
	distribute := false
	response := Response{
		Status:      "OK",
		ServiceName: serviceName,
	}
	if len(req.URL.Query().Get("distribute")) > 0 {
		distribute, _ = strconv.ParseBool(req.URL.Query().Get("distribute"))
		if distribute {
			response.Distribute = distribute
			response.Message = DISTRIBUTED
		}
	}
	if len(serviceName) == 0 {
		response.Status = "NOK"
		response.Message = "The serviceName query is mandatory"
		w.WriteHeader(http.StatusBadRequest)
	} else if distribute {
		srv := server.Serve{}
		if status, err := srv.SendDistributeRequests(req, m.Port, m.ServiceName); err != nil || status >= 300 {
			m.writeInternalServerError(w, &response, err.Error())
		} else {
			response.Message = DISTRIBUTED
			w.WriteHeader(http.StatusOK)
		}
	} else {
		logPrintf("Processing remove request %s", req.URL.Path)
		aclName := req.URL.Query().Get("aclName")
		action := NewRemove(
			serviceName,
			aclName,
			m.BaseReconfigure.ConfigsPath,
			m.BaseReconfigure.TemplatesPath,
			m.ConsulAddresses,
			m.InstanceName,
			m.Mode,
		)
		action.Execute([]string{})
		w.WriteHeader(http.StatusOK)
	}
	httpWriterSetContentType(w, "application/json")
	js, _ := json.Marshal(response)
	w.Write(js)
}

func (m *Serve) config(w http.ResponseWriter, req *http.Request) {
	httpWriterSetContentType(w, "text/html")
	out, err := proxy.Instance.ReadConfig()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	w.Write([]byte(out))
}

func (m *Serve) setConsulAddresses() {
	m.ConsulAddresses = []string{}
	if len(os.Getenv("CONSUL_ADDRESS")) > 0 {
		for _, address := range strings.Split(os.Getenv("CONSUL_ADDRESS"), ",") {
			if !strings.HasPrefix(address, "http") {
				address = fmt.Sprintf("http://%s", address)
			}
			m.ConsulAddresses = append(m.ConsulAddresses, address)
		}
	}
}

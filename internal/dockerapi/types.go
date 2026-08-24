package dockerapi

type ContainerInspect struct {
	ID              string                   `json:"Id"`
	Name            string                   `json:"Name"`
	Config          ContainerConfig          `json:"Config"`
	HostConfig      HostConfigInspect        `json:"HostConfig"`
	Mounts          []Mount                  `json:"Mounts"`
	NetworkSettings ContainerNetworkSettings `json:"NetworkSettings"`
	State           ContainerState           `json:"State"`
}

type ContainerConfig struct {
	Hostname   string            `json:"Hostname,omitempty"`
	Domainname string            `json:"Domainname,omitempty"`
	User       string            `json:"User,omitempty"`
	Env        []string          `json:"Env,omitempty"`
	Image      string            `json:"Image,omitempty"`
	Labels     map[string]string `json:"Labels,omitempty"`
	WorkingDir string            `json:"WorkingDir,omitempty"`
}

type HostConfigInspect struct {
	DNS        []string `json:"Dns"`
	DNSSearch  []string `json:"DnsSearch"`
	DNSOptions []string `json:"DnsOptions"`
	ExtraHosts []string `json:"ExtraHosts"`
	ShmSize    int64    `json:"ShmSize"`
}

type ContainerNetworkSettings struct {
	Networks map[string]*EndpointSettings `json:"Networks"`
}

type EndpointSettings struct {
	NetworkID  string            `json:"NetworkID,omitempty"`
	IPAddress  string            `json:"IPAddress,omitempty"`
	GlobalIPv6 string            `json:"GlobalIPv6Address,omitempty"`
	Aliases    []string          `json:"Aliases,omitempty"`
	Gateway    string            `json:"Gateway,omitempty"`
	MacAddress string            `json:"MacAddress,omitempty"`
	DriverOpts map[string]string `json:"DriverOpts,omitempty"`
}

type ContainerState struct {
	Status  string `json:"Status"`
	Running bool   `json:"Running"`
	Pid     int    `json:"Pid"`
}

type Mount struct {
	Type        string `json:"Type"`
	Name        string `json:"Name"`
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
	Driver      string `json:"Driver"`
	Mode        string `json:"Mode"`
	RW          bool   `json:"RW"`
	Propagation string `json:"Propagation"`
}

type ImageInspect struct {
	ID     string      `json:"Id"`
	Config ImageConfig `json:"Config"`
}

type ImageConfig struct {
	Env          []string            `json:"Env"`
	ExposedPorts map[string]struct{} `json:"ExposedPorts"`
}

type ContainerSummary struct {
	ID      string            `json:"Id"`
	Labels  map[string]string `json:"Labels"`
	State   string            `json:"State"`
	Created int64             `json:"Created"`
}

type ContainerCreateRequest struct {
	Image            string            `json:"Image"`
	Env              []string          `json:"Env,omitempty"`
	Labels           map[string]string `json:"Labels"`
	HostConfig       ChildHostConfig   `json:"HostConfig"`
	NetworkingConfig *NetworkingConfig `json:"NetworkingConfig,omitempty"`
}

type ChildHostConfig struct {
	Mounts         []MountSpec `json:"Mounts,omitempty"`
	NetworkMode    string      `json:"NetworkMode,omitempty"`
	DNS            []string    `json:"Dns,omitempty"`
	DNSSearch      []string    `json:"DnsSearch,omitempty"`
	DNSOptions     []string    `json:"DnsOptions,omitempty"`
	ExtraHosts     []string    `json:"ExtraHosts,omitempty"`
	ShmSize        int64       `json:"ShmSize,omitempty"`
	SecurityOpt    []string    `json:"SecurityOpt,omitempty"`
	Privileged     bool        `json:"Privileged"`
	ReadonlyRootfs bool        `json:"ReadonlyRootfs"`
	AutoRemove     bool        `json:"AutoRemove"`
}

type MountSpec struct {
	Type        string `json:"Type"`
	Source      string `json:"Source,omitempty"`
	Target      string `json:"Target"`
	ReadOnly    bool   `json:"ReadOnly,omitempty"`
	Consistency string `json:"Consistency,omitempty"`
}

type NetworkingConfig struct {
	EndpointsConfig map[string]*EndpointSettings `json:"EndpointsConfig"`
}

type ContainerCreateResponse struct {
	ID       string   `json:"Id"`
	Warnings []string `json:"Warnings"`
}

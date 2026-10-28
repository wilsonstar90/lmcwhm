// Package appstore installs runtimes, databases and tools as Docker images or
// containers, one per version (aaPanel "App Store" / 寶塔軟件商店).
package appstore

import "strings"

type Kind string

const (
	KindRuntime  Kind = "runtime"  // image only; website projects run on it
	KindDatabase Kind = "database" // long-running service on a loopback port
	KindTool     Kind = "tool"     // long-running helper service
)

type Version struct {
	Version string `json:"version"`
	Image   string `json:"image"`
	EOL     bool   `json:"eol,omitempty"`
}

type App struct {
	Key      string    `json:"key"`
	Name     string    `json:"name"`
	Kind     Kind      `json:"kind"`
	Versions []Version `json:"versions"`
	// Port is the preferred loopback port of a service; the next free one is used when taken.
	Port int `json:"port,omitempty"`
	// MinMemMB refuses the install on smaller hosts.
	MinMemMB int `json:"min_mem_mb,omitempty"`
	// AMD64Only images have no arm64 build.
	AMD64Only bool `json:"amd64_only,omitempty"`
	// Databases says the engine supports creating databases + users.
	Databases bool `json:"databases,omitempty"`
}

// Catalog lists every installable app. The order is the display order.
var Catalog = []App{
	{Key: "node", Name: "Node.js", Kind: KindRuntime, Versions: []Version{
		{"24", "node:24-slim", false}, {"22", "node:22-slim", false}, {"20", "node:20-slim", false}, {"18", "node:18-slim", true},
	}},
	{Key: "python", Name: "Python", Kind: KindRuntime, Versions: []Version{
		{"3.13", "python:3.13-slim", false}, {"3.12", "python:3.12-slim", false}, {"3.11", "python:3.11-slim", false},
		{"3.10", "python:3.10-slim", false}, {"3.9", "python:3.9-slim", true},
	}},
	{Key: "java", Name: "Java (Temurin JDK)", Kind: KindRuntime, Versions: []Version{
		{"21", "eclipse-temurin:21-jdk", false}, {"17", "eclipse-temurin:17-jdk", false},
		{"11", "eclipse-temurin:11-jdk", false}, {"8", "eclipse-temurin:8-jdk", false},
	}},
	{Key: "mysql", Name: "MySQL", Kind: KindDatabase, Port: 3306, Databases: true, Versions: []Version{
		{"8.4", "mysql:8.4", false}, {"8.0", "mysql:8.0", false}, {"5.7", "mysql:5.7", true},
	}},
	{Key: "mariadb", Name: "MariaDB", Kind: KindDatabase, Port: 3306, Databases: true, Versions: []Version{
		{"11.4", "mariadb:11.4", false}, {"10.11", "mariadb:10.11", false}, {"10.6", "mariadb:10.6", false},
	}},
	{Key: "postgres", Name: "PostgreSQL", Kind: KindDatabase, Port: 5432, Databases: true, Versions: []Version{
		{"17", "postgres:17", false}, {"16", "postgres:16", false}, {"15", "postgres:15", false},
		{"14", "postgres:14", false}, {"13", "postgres:13", true},
	}},
	{Key: "mongodb", Name: "MongoDB", Kind: KindDatabase, Port: 27017, Databases: true, Versions: []Version{
		{"7.0", "mongo:7.0", false}, {"6.0", "mongo:6.0", false},
	}},
	{Key: "sqlserver", Name: "SQL Server", Kind: KindDatabase, Port: 1433, Databases: true, MinMemMB: 2000, AMD64Only: true, Versions: []Version{
		{"2022", "mcr.microsoft.com/mssql/server:2022-latest", false}, {"2019", "mcr.microsoft.com/mssql/server:2019-latest", false},
	}},
	{Key: "redis", Name: "Redis", Kind: KindDatabase, Port: 6379, Versions: []Version{
		{"7.4", "redis:7.4", false}, {"7.2", "redis:7.2", false}, {"6.2", "redis:6.2", true},
	}},
	{Key: "memcached", Name: "Memcached", Kind: KindTool, Port: 11211, Versions: []Version{
		{"1.6", "memcached:1.6", false},
	}},
	{Key: "adminer", Name: "Adminer", Kind: KindTool, Port: 8090, Versions: []Version{
		{"4", "adminer:4", false},
	}},
}

// Lookup finds an app and version in the catalog.
func Lookup(key, version string) (App, Version, bool) {
	for _, a := range Catalog {
		if a.Key != key {
			continue
		}
		for _, v := range a.Versions {
			if v.Version == version {
				return a, v, true
			}
		}
	}
	return App{}, Version{}, false
}

// ContainerName maps ("mysql", "8.4") to "lmcwhm-mysql84".
func ContainerName(key, version string) string {
	return "lmcwhm-" + key + strings.ReplaceAll(version, ".", "")
}

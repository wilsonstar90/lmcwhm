export interface User {
  id: number
  username: string
  totp_enabled: boolean
  is_admin: boolean
}

export type SSLMode = 'off' | 'selfsigned' | 'custom'

export interface PanelSettings {
  panel_alias: string
  panel_port: number
  listen_ipv6: boolean
  security_entry: string
  ssl_mode: SSLMode
  bind_domain: string
  allowed_ips: string[] | null
  session_timeout: number
  login_max_fails: number
  login_lock_mins: number
  basic_auth_user: string
  basic_auth_password?: string
  language: string
  timezone: string
}

export interface Overview {
  hostname: string
  os: string
  platform: string
  kernel: string
  arch: string
  uptime: number
  cpu_model: string
  cpu_cores: number
  cpu_percent: number
  per_core: number[]
  load: [number, number, number]
  mem_total: number
  mem_used: number
  swap_total: number
  swap_used: number
  disks: { mount: string; fs: string; total: number; used: number }[] | null
  net_sent: number
  net_recv: number
  sampled_at: number
}

export interface CertInfo {
  subject: string
  issuer: string
  dns_names: string[]
  not_after: string
}

export type SiteType = 'php' | 'static' | 'node' | 'python' | 'java' | 'proxy'

export interface Site {
  id: number
  domain: string
  aliases: string[]
  type: SiteType
  php_version: string
  root: string
  stopped: boolean
  force_https: boolean
  hsts: boolean
  ssl_mode: 'none' | 'acme' | 'custom'
  ssl_challenge: 'http' | 'dns'
  ssl_wildcard: boolean
  cert_issuer: string
  cert_not_after: number
  cert_error: string
  dns_managed: boolean
  dns_proxied: boolean
  remark: string
  created_at: number
  runtime_version: string
  command: string
  app_port: number
  proxy_target: string
  passthrough: boolean
}

export interface ServiceStatus {
  name: string
  image: string
  state: string
}

export interface WebStatus {
  status: { docker: boolean; installed: boolean; services: ServiceStatus[] | null; php: ServiceStatus[] | null }
  php_versions: string[]
}

export interface WebConfig {
  has_cf_token: boolean
  acme_email: string
  acme_ca: 'letsencrypt' | 'letsencrypt-staging' | 'litessl' | 'custom'
  acme_directory: string
  eab_kid: string
  has_eab_hmac: boolean
  server_ipv4: string
  server_ipv6: string
}

export interface Task {
  id: string
  name: string
  status: 'running' | 'success' | 'failed'
  log: string[]
  error?: string
}

export type AppKind = 'runtime' | 'database' | 'tool'

export interface CatalogApp {
  key: string
  name: string
  kind: AppKind
  versions: { version: string; image: string; eol?: boolean }[]
  port?: number
  min_mem_mb?: number
  amd64_only?: boolean
  databases?: boolean
}

export interface InstalledApp {
  id: number
  app: string
  name: string
  kind: AppKind
  version: string
  image: string
  port: number
  container: string
  state: string
  has_config: boolean
  databases: boolean
  created_at: number
}

export interface Database {
  id: number
  app_id: number
  engine: string
  version: string
  name: string
  username: string
  password: string
  host: string
  port: number
  remark: string
  created_at: number
}

export interface Container {
  id: string
  name: string
  image: string
  state: string
  status: string
  ports: string
  created: string
  managed: boolean
  project: string
  cpu: string
  mem: string
  mem_perc: string
  net_io: string
  block_io: string
  host_ports: number[]
}

export interface DockerImage {
  id: string
  repository: string
  tag: string
  size: string
  created: string
}

export interface DockerVolume {
  name: string
  driver: string
  mountpoint: string
}

export interface DockerNetwork {
  id: string
  name: string
  driver: string
  scope: string
}

export interface ComposeProject {
  name: string
  dir: string
  running: number
  services: number
  status: string
}

export interface FileEntry {
  name: string
  path: string
  is_dir: boolean
  size: number
  mode: string
  mtime: number
  owner: string
  group: string
  link?: string
}

export interface TrashItem {
  id: string
  path: string
  is_dir: boolean
  size: number
  deleted_at: number
}

export interface Route {
  host: string
  wildcard: boolean
  primary: boolean
  site_id: number
  domain: string
  type: SiteType
  mode: 'terminate' | 'passthrough'
  upstream: string
  stopped: boolean
  container: string
  force_https: boolean
  hsts: boolean
  cert_issuer: string
  cert_not_after: number
  cert_covers: boolean
  dns_managed: boolean
  dns_proxied: boolean
  overrides?: string[]
  health: '' | 'up' | 'down'
  dns: '' | 'ok' | 'other' | 'none'
  dns_addrs?: string[]
  warnings: string[]
}

export interface RouteResolution {
  host: string
  pattern: string
  match: 'exact' | 'wildcard' | 'default'
  route?: Route
}

export interface FirewallRule {
  id: number
  action: 'accept' | 'drop'
  proto: 'tcp' | 'udp' | 'both'
  ports: string
  source: string
  remark: string
  enabled: boolean
  created_at: number
}

export interface FirewallIP {
  id: number
  ip: string
  list: 'block' | 'allow'
  source: 'manual' | 'crowdsec' | 'waf'
  remark: string
  expires_at: number
  created_at: number
}

export interface SyncReport {
  at: number
  cloudflare?: string[]
  linode?: string
  errors?: string[]
}

export interface FirewallView {
  enabled: boolean
  policy: 'accept' | 'drop'
  ping: boolean
  protect: boolean
  linode_token?: string
  linode_firewall: number
  linode_sync: boolean
  linode_ports: boolean
  cf_sync: boolean
  has_linode_token: boolean
  available: boolean
  pending: boolean
  confirm_secs: number
  protected_ports: number[]
  live: string
  last_sync: SyncReport
  has_cf_token: boolean
  rules: FirewallRule[]
}

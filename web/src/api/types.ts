export interface User {
  id: number
  username: string
  totp_enabled: boolean
  is_admin: boolean
  perms?: string[]
  sites?: number[]
  enabled?: boolean
  remark?: string
  created_at?: number
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
  unauth_status: number
  site_root: string
  backup_dir: string
  logo_image: boolean
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
  nics: { name: string; sent: number; recv: number }[] | null
  disk_io: { name: string; read_bytes: number; write_bytes: number; read_count: number; write_count: number; read_time: number; write_time: number }[] | null
  sampled_at: number
}

export interface SysDetail {
  load: { avg: [number, number, number]; procs_running: number; procs_total: number; times: Record<string, number> | null }
  cpu: { model: string; sockets: number; cores: number; threads: number; per_core: number[] | null; percent: number }
  mem: { total: number; used: number; free: number; shared: number; available: number; buffers: number; cached: number }
  disks: { mount: string; device: string; fs: string; total: number; used: number; free: number; percent: number; inodes_total: number; inodes_used: number; inodes_free: number; inodes_percent: number }[] | null
}

export interface CertInfo {
  subject: string
  issuer: string
  dns_names: string[]
  not_after: string
}

export type SiteType = 'php' | 'static' | 'node' | 'python' | 'java' | 'tomcat' | 'proxy'

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
  waf_off?: boolean
  waf_exclude?: string[] | null
  advanced?: SiteAdvanced
  target: DomainTarget
  target_site: number
  container: string
  redirect: string
  redirect_code: number
  redirect_path: boolean
  target_subdir?: string
  stop_mode?: '404' | 'page'
  stop_page?: number
  expires_at?: number
  category?: string
  run_dir?: string
  rewrite?: string
  index_files?: string[]
  custom_conf?: string
}

// What a domain entry serves (its own website, another website, a container, …).
export type DomainTarget = 'site' | 'link' | 'docker' | 'url' | 'passthrough' | 'redirect'

export interface PortForward {
  id: number
  proto: 'tcp' | 'udp' | 'both'
  listen: number
  target_host: string
  target_port: number
  source: string
  remark: string
  enabled: boolean
  created_at: number
  packets: number
  bytes: number
}

export interface SiteAdvanced {
  autoindex: boolean
  allow_ips: string[]
  deny_ips: string[]
  deny_ua: string[]
  rate_limit: number
  rate_burst: number
  conn_limit: number
  error_pages: boolean
  block_xmlrpc: boolean
  open_basedir: boolean
  hotlink: string[]
  max_body_mb: number
  redirects: { from: string; to: string; code: number; keep_path?: boolean }[]
  varnish?: boolean
  varnish_ttl?: number
  varnish_exclude?: string[]
  no_access_log?: boolean
  basic_auth?: AuthUser | null
  auth_dirs?: (AuthUser & { name: string; path: string })[]
  server_conn?: number
  limit_rate_kb?: number
  hotlink_ext?: string[]
  hotlink_deny_empty?: boolean
  proxies?: PathProxy[]
}

// A user for HTTP password protection; the password is only sent, never returned.
export interface AuthUser {
  user: string
  password?: string
}

export interface PathProxy {
  name: string
  path: string
  target: string
  host: string
  enabled: boolean
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
  oom_protect?: boolean
  full_version?: string
  build_mode?: 'fast' | 'source'
  build_args?: string
  build_cflags?: string
  source_no?: string
  source_minutes?: number
  source_defaults?: string
}

export interface AppUpdate {
  key: string
  name: string
  image: string
  current: string
  latest?: string
  available: boolean
  checked_at: number
  error?: string
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
  created_at: number
  usage: DockerUsage | null
}

export interface DockerUsage {
  cpu: number
  mem_used: number
  mem_limit: number
  net_rx: number
  net_tx: number
  blk_read: number
  blk_write: number
  pids: number
  at: number
}

export interface DockerOverview {
  containers: { total: number; running: number; size: string }
  compose: { total: number; running: number }
  images: { total: number; size: string }
  volumes: { total: number; size: string }
  networks: { total: number }
  registries: { total: number }
}

export interface ContainerInfo {
  id: string
  name: string
  image: string
  image_id: string
  digests: string[]
  state: string
  created: number
  started_at: number
  finished_at: number
  ports: { host_ip: string; host_port: number; container_port: string }[]
  host_ports: number[]
  networks: { name: string; ip: string; ipv6: string; gateway: string; mac: string }[]
  cmd: string[]
  entrypoint: string[]
  env: string[]
  mounts: { type: string; name: string; source: string; destination: string; rw: boolean }[]
  restart: string
  max_retry: number
  working_dir: string
  user: string
  hostname: string
  managed: boolean
  project: string
  has_log_file: boolean
}

export interface ContainerSpec {
  name: string
  image: string
  ports: string[]
  env: string[]
  volumes: string[]
  restart: string
  network: string
  command: string
  cpus: string
  memory: string
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
  remark?: string
  auto?: { code: string; arg?: string }
  protected?: boolean
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
  dns: '' | 'ok' | 'other' | 'none' | 'cloudflare'
  dns_addrs?: string[]
  warnings: string[]
  target: DomainTarget
  target_site: number
  target_name?: string
  redirect?: string
  remark: string
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
  system?: 'ssh' | 'panel' | 'web'
}

export interface FirewallIP {
  id: number
  ip: string
  list: 'block' | 'allow' | 'trust'
  source: string // manual | crowdsec | mcp | feed:<id>
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

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

export interface Site {
  id: number
  domain: string
  aliases: string[]
  type: 'php' | 'static'
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

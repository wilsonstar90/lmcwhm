import type { FirewallView } from './types'

// fwConfig is the part of the firewall view that PUT /firewall takes; the view
// also carries the live ruleset, which can be far larger than a request may be.
export const fwConfig = (v: FirewallView) => ({
  enabled: v.enabled,
  policy: v.policy,
  ping: v.ping,
  linode_firewall: v.linode_firewall,
  linode_sync: v.linode_sync,
  linode_ports: v.linode_ports,
  cf_sync: v.cf_sync,
})

<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NTabPane, NTabs } from 'naive-ui'
import SecFirewall from '../components/SecFirewall.vue'
import SecIPs from '../components/SecIPs.vue'
import SecCloud from '../components/SecCloud.vue'
import SecWAF from '../components/SecWAF.vue'
import SecCrowdSec from '../components/SecCrowdSec.vue'
import SecScan from '../components/SecScan.vue'
import SecMalware from '../components/SecMalware.vue'
import SecTamper from '../components/SecTamper.vue'
import { useRoute } from 'vue-router'

const { t } = useI18n()
const tab = ref((useRoute().query.tab as string) || 'firewall')
</script>

<template>
  <n-tabs v-model:value="tab" type="line" animated>
    <n-tab-pane name="firewall" :tab="t('sec.firewall')" display-directive="show:lazy"><SecFirewall /></n-tab-pane>
    <n-tab-pane name="ips" :tab="t('sec.ips')" display-directive="show:lazy"><SecIPs /></n-tab-pane>
    <n-tab-pane name="cloud" :tab="t('sec.cloud')" display-directive="show:lazy"><SecCloud /></n-tab-pane>
    <n-tab-pane name="waf" tab="WAF" display-directive="show:lazy"><SecWAF /></n-tab-pane>
    <n-tab-pane name="crowdsec" tab="CrowdSec" display-directive="show:lazy"><SecCrowdSec /></n-tab-pane>
    <n-tab-pane name="scan" :tab="t('sec.scan')" display-directive="show:lazy"><SecScan /></n-tab-pane>
    <n-tab-pane name="malware" :tab="t('sec.malware')" display-directive="show:lazy"><SecMalware /></n-tab-pane>
    <n-tab-pane name="tamper" :tab="t('sec.tamper')" display-directive="show:lazy"><SecTamper /></n-tab-pane>
  </n-tabs>
</template>

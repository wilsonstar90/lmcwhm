<script setup lang="ts">
// The panel's logo: the uploaded image, or the alias as text.
import { useSession } from '../stores/session'

const session = useSession()
</script>

<template>
  <img v-if="session.logoImage" class="logo-img" :src="`/api/v1/public/logo?v=${session.logoRev}`" :alt="session.alias" />
  <span v-else class="logo-text">{{ session.alias }}</span>
</template>

<style scoped>
.logo-img {
  display: block;
  max-height: 32px;
  max-width: 150px;
  object-fit: contain;
}
.logo-text {
  font-weight: 600;
  font-size: 18px;
  color: #20a53a;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 150px;
}
</style>

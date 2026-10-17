<script setup>
import { computed } from 'vue'
import AppIcon from './AppIcon.vue'
import SiteText from './SiteText.vue'
import { site, listOf } from '../site/store.js'
import { accentVars } from '../site/schema.js'

// version comes from the server's own /api/system/info, so the footer reports
// the running binary rather than a number baked into the bundle.
defineProps({
  me: { type: Object, default: null },
  version: { type: String, default: '' },
})

const columns = computed(() => listOf('footer.columns'))
const capabilities = computed(() => listOf('footer.capabilities.items'))
// The footer wears the same accent as the login page; nothing else is restyled.
const footStyle = computed(() => accentVars(site.doc.site.accent))
</script>

<template>
  <footer class="foot" :style="footStyle">
    <div class="inner">
      <div class="col brand-col">
        <div class="brand">
          <span class="logo"><AppIcon name="play" :size="12" filled /></span>
          <span class="wordmark">
            <SiteText k="footer.wordmark.name" /><SiteText k="footer.wordmark.suffix" class="wm-suffix" />
          </span>
        </div>
        <SiteText k="footer.about" as="p" class="tiny" multiline />
        <div class="status">
          <span class="pulse"></span>
          <span class="tiny">
            <SiteText k="footer.status" as="span" /><template v-if="version"> · {{ version }}</template>
          </span>
        </div>
      </div>

      <div v-for="(col, ci) in columns" :key="ci" class="col">
        <SiteText :k="`footer.columns.${ci}.heading`" as="div" class="h" />
        <router-link v-for="(l, li) in col.links" :key="li" :to="l.to || '/'">
          <SiteText :k="`footer.columns.${ci}.links.${li}.label`" passive />
        </router-link>
      </div>

      <div class="col">
        <SiteText k="footer.capabilities.heading" as="div" class="h" />
        <div v-for="(it, i) in capabilities" :key="i" class="tiny line">
          <AppIcon name="check" :size="13" />
          <SiteText :k="`footer.capabilities.items.${i}`" passive />
        </div>
      </div>
    </div>
    <div class="base">
      <SiteText k="footer.copyright" as="span" class="tiny" />
      <span class="spacer"></span>
      <span v-if="me" class="tiny">{{ me.name }}</span>
    </div>
  </footer>
</template>

<style scoped>
.foot {
  margin-top: clamp(40px, 6vw, 88px);
  border-top: 1px solid var(--line-1);
  background: linear-gradient(180deg, rgba(255, 255, 255, 0.018), transparent);
}
.inner {
  max-width: var(--page-max);
  margin: 0 auto;
  padding: clamp(32px, 4vw, 56px) var(--gutter) clamp(20px, 3vw, 32px);
  display: grid;
  grid-template-columns: minmax(280px, 1.6fr) repeat(auto-fit, minmax(130px, 1fr));
  gap: var(--sp-6) var(--sp-5);
}
.col { display: flex; flex-direction: column; gap: 9px; align-items: flex-start; }
.col .h {
  font-size: var(--fs-12);
  font-weight: 700;
  letter-spacing: 0.1em;
  text-transform: uppercase;
  color: var(--text-4);
  margin-bottom: 3px;
}
.col a { font-size: var(--fs-13); color: var(--text-3); transition: color var(--t-fast); }
.col a:hover { color: var(--text-1); }
.brand { display: flex; align-items: center; gap: 9px; margin-bottom: 6px; }
.logo {
  display: grid; place-items: center;
  width: 26px; height: 26px;
  border-radius: 9px;
  background: linear-gradient(150deg, var(--brand-2), var(--brand) 70%);
  color: var(--brand-ink);
}
.wordmark { font-family: var(--font-display); font-size: var(--fs-15); font-weight: 800; letter-spacing: -0.03em; }
.wordmark .wm-suffix { color: var(--text-3); font-weight: 600; }
.brand-col p { max-width: 46ch; line-height: 1.7; margin: 0; }
.status { display: flex; align-items: center; gap: 8px; margin-top: 4px; }
.pulse {
  width: 7px; height: 7px; border-radius: 50%;
  background: var(--ok);
  box-shadow: 0 0 0 0 rgba(74, 222, 128, 0.6);
  animation: pulse 2.4s infinite;
}
@keyframes pulse {
  70% { box-shadow: 0 0 0 8px rgba(74, 222, 128, 0); }
  100% { box-shadow: 0 0 0 0 rgba(74, 222, 128, 0); }
}
.line { display: flex; align-items: center; gap: 7px; color: var(--text-3); }
.line svg { color: var(--ok); }
.base {
  max-width: var(--page-max);
  margin: 0 auto;
  padding: 16px var(--gutter) calc(20px + env(safe-area-inset-bottom));
  border-top: 1px solid var(--line-1);
  display: flex;
  align-items: center;
  gap: 12px;
}
@media (max-width: 900px) {
  .inner { grid-template-columns: 1fr 1fr; }
  .brand-col { grid-column: 1 / -1; }
}
@media (max-width: 720px) {
  .foot { margin-bottom: 0; }
  .base { padding-bottom: calc(72px + env(safe-area-inset-bottom)); }
}
</style>

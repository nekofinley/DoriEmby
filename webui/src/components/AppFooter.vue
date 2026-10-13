<script setup>
import AppIcon from './AppIcon.vue'

// version comes from the server's own /api/system/info, so the footer reports
// the running binary rather than a number baked into the bundle.
defineProps({
  me: { type: Object, default: null },
  version: { type: String, default: '' },
})
</script>

<template>
  <footer class="foot">
    <div class="inner">
      <div class="col brand-col">
        <div class="brand">
          <span class="logo"><AppIcon name="play" :size="12" filled /></span>
          <span class="wordmark">Doriya<span>Media</span></span>
        </div>
        <p class="tiny">
          轻量自建流媒体服务器:挂载网盘、扫描媒体库、TMDB / Bangumi 刮削,
          针对 iOS Safari 自动选择 302 直连、代理或 HLS 转封装播放。
        </p>
        <div class="status">
          <span class="pulse"></span>
          <span class="tiny">本地服务运行中<template v-if="version"> · {{ version }}</template></span>
        </div>
      </div>

      <div class="col">
        <div class="h">浏览</div>
        <router-link to="/">首页</router-link>
        <router-link to="/browse">文件浏览</router-link>
      </div>

      <div class="col">
        <div class="h">管理</div>
        <router-link to="/settings">存储源 / 媒体库</router-link>
        <router-link to="/settings">刮削与播放</router-link>
      </div>

      <div class="col">
        <div class="h">播放能力</div>
        <div class="tiny line"><AppIcon name="check" :size="13" /> 302 直连 · 不占带宽</div>
        <div class="tiny line"><AppIcon name="check" :size="13" /> HLS 转封装 · 视频直拷</div>
        <div class="tiny line"><AppIcon name="check" :size="13" /> iOS Safari 自动适配</div>
      </div>
    </div>
    <div class="base">
      <span class="tiny">© {{ new Date().getFullYear() }} DoriyaMedia · 仅供个人媒体库使用</span>
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
  grid-template-columns: minmax(280px, 1.6fr) repeat(3, minmax(140px, 1fr));
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
  background: linear-gradient(150deg, var(--brand-2), #d98a12 70%);
  color: var(--brand-ink);
}
.wordmark { font-family: var(--font-display); font-size: var(--fs-15); font-weight: 800; letter-spacing: -0.03em; }
.wordmark span { color: var(--text-3); font-weight: 600; }
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

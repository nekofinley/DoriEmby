<script setup>
/** 文案 tab — every word the login page says. */
import { listOf, setField, addItem, removeItem, moveItem, hoverKey } from '../../site/store.js'
import { FEATURE_ICONS } from '../../site/schema.js'
import AppIcon from '../AppIcon.vue'
import PanelGroup from './PanelGroup.vue'
import PanelField from './PanelField.vue'

const features = () => listOf('login.features')

function pickIcon(i, e) {
  setField(`login.features.${i}.icon`, e.target.value)
}
function addFeature() {
  addItem('login.features', { icon: 'star', t: '新亮点', d: '一句话说明它好在哪' })
}
function hot(k) {
  hoverKey.value = k
}
function cold() {
  hoverKey.value = ''
}
</script>

<template>
  <PanelGroup title="站点" icon="globe" open>
    <PanelField k="site.title" label="浏览器标题 / 站点名" hint="标签页标题,也会显示在页脚版权占位符里" />
  </PanelGroup>

  <PanelGroup title="品牌" icon="wand">
    <div class="st-pair">
      <PanelField k="login.brand.name" label="名称" />
      <PanelField k="login.brand.suffix" label="后缀(高亮部分)" />
    </div>
  </PanelGroup>

  <PanelGroup title="主视觉" icon="type" open>
    <PanelField k="login.pitch.eyebrow" label="小标签" hint="标题上方的一行小字" />
    <PanelField k="login.pitch.title" label="大标题" />
    <PanelField k="login.pitch.body" label="说明段落" :rows="3" />
  </PanelGroup>

  <PanelGroup title="亮点卡片" icon="sparkles">
    <div v-for="(f, i) in features()" :key="i" class="st-item" @mouseenter="hot(`login.features.${i}.t`)" @mouseleave="cold">
      <div class="st-item-head">
        <select class="st-input st-icon-pick" :value="f.icon" @change="pickIcon(i, $event)">
          <option v-for="ic in FEATURE_ICONS" :key="ic" :value="ic">{{ ic }}</option>
        </select>
        <span class="st-item-no">{{ i + 1 }}</span>
        <span class="spacer" />
        <button class="st-mini" title="上移" :disabled="i === 0" @click="moveItem('login.features', i, -1)"><AppIcon name="chevronUp" :size="14" /></button>
        <button class="st-mini" title="下移" :disabled="i === features().length - 1" @click="moveItem('login.features', i, 1)"><AppIcon name="chevronDown" :size="14" /></button>
        <button class="st-mini danger" title="删除" @click="removeItem('login.features', i)"><AppIcon name="trash" :size="14" /></button>
      </div>
      <PanelField :k="`login.features.${i}.t`" label="标题" />
      <PanelField :k="`login.features.${i}.d`" label="说明" />
    </div>
    <button class="st-add" @click="addFeature"><AppIcon name="plus" :size="14" /> 添加一张卡片</button>
  </PanelGroup>

  <PanelGroup title="登录表单" icon="lock">
    <p class="st-note">表单会在四种状态下切换文案:登录、注册、找回密码、初始化管理员。</p>
    <PanelField k="login.form.login.title" label="登录 · 标题" />
    <PanelField k="login.form.login.desc" label="登录 · 说明" />
    <PanelField k="login.form.register.title" label="注册 · 标题" />
    <PanelField k="login.form.register.desc" label="注册 · 说明" />
    <PanelField k="login.form.forgot.title" label="找回密码 · 标题" />
    <PanelField k="login.form.forgot.desc" label="找回密码 · 说明" />
    <PanelField k="login.form.setup.title" label="初始化 · 标题" />
    <PanelField k="login.form.setup.desc" label="初始化 · 说明" />
  </PanelGroup>

  <PanelGroup title="按钮与字段" icon="sliders">
    <PanelField k="login.form.submit.login" label="按钮 · 登录" />
    <PanelField k="login.form.submit.register" label="按钮 · 注册" />
    <PanelField k="login.form.submit.forgot" label="按钮 · 找回密码" />
    <PanelField k="login.form.submit.setup" label="按钮 · 初始化" />
    <PanelField k="login.form.tabLogin" label="切换标签 · 登录" />
    <PanelField k="login.form.tabRegister" label="切换标签 · 注册" />
    <PanelField k="login.form.forgotLink" label="链接 · 忘记密码" />
    <PanelField k="login.form.backToLogin" label="链接 · 返回登录" />
    <PanelField k="login.form.labels.name" label="字段 · 用户名" />
    <PanelField k="login.form.labels.nameOrMail" label="字段 · 用户名或邮箱" />
    <PanelField k="login.form.labels.password" label="字段 · 密码" />
    <PanelField k="login.form.labels.email" label="字段 · 邮箱" />
    <PanelField k="login.form.labels.confirm" label="字段 · 确认密码" />
  </PanelGroup>

  <PanelGroup title="登录页底部小字" icon="link">
    <PanelField k="login.form.foot" label="表单下方那一行" hint="支持占位符 {host} {year} {version} {user}" />
  </PanelGroup>
</template>

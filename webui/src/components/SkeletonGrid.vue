<script setup>
/** Shimmering poster-grid placeholder shown while a library loads. */
defineProps({ count: { type: Number, default: 12 } })
</script>

<template>
  <div class="grid-auto">
    <div v-for="i in count" :key="i" class="cell">
      <div class="sk poster" />
      <div class="sk line mt-3" style="width:78%" />
      <div class="sk line mt-2" style="width:44%;height:9px" />
    </div>
  </div>
</template>

<style scoped>
.cell { display: block; }
</style>

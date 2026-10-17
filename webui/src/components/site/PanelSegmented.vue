<script setup>
/** A segmented control for the layout choices. */
defineProps({
  modelValue: { type: String, default: '' },
  options: { type: Array, required: true },
})
defineEmits(['update:modelValue'])
</script>

<template>
  <div class="st-seg" role="radiogroup">
    <button
      v-for="o in options" :key="o.id" type="button" role="radio"
      :aria-checked="o.id === modelValue" :class="{ on: o.id === modelValue }"
      @click="$emit('update:modelValue', o.id)"
    >{{ o.label }}</button>
  </div>
</template>

<template>
  <div v-if="description || technicalDetails" class="quality-explanation small">
    <div v-if="description" class="quality-description">{{ description }}</div>
    <details v-if="technicalDetails" :key="technicalDetails">
      <summary>{{ technicalLabel }}</summary>
      <div class="quality-technical">{{ technicalDetails }}</div>
    </details>
  </div>
</template>

<script setup lang="ts">
defineProps<{
  description?: string;
  technicalDetails?: string;
  technicalLabel: string;
}>();
</script>

<style scoped>
.quality-explanation {
  margin-top: 0.75em;
  line-height: 1.65;
  overflow-wrap: anywhere;
}

.quality-description,
.quality-technical {
  white-space: pre-line;
}

.quality-explanation details {
  margin-top: 0.65em;
}

.quality-explanation summary {
  cursor: pointer;
}

.quality-technical {
  margin-top: 0.5em;
  color: var(--textSecondary);
}
</style>

<template>
  <div v-if="description || technicalDetails" class="quality-explanation small">
    <div v-if="description" class="quality-description">
      <div
        v-for="(line, index) in descriptionLines"
        :key="index"
        class="quality-fact"
        :class="{ 'quality-fact-plain': !line.label }"
      >
        <strong v-if="line.label">{{ line.label }}</strong
        ><span>{{ line.value }}</span>
      </div>
    </div>
    <details v-if="technicalDetails" :key="technicalDetails">
      <summary>{{ technicalLabel }}</summary>
      <div class="quality-technical">{{ technicalDetails }}</div>
    </details>
  </div>
</template>

<script setup lang="ts">
import { computed } from "vue";

const props = defineProps<{
  description?: string;
  technicalDetails?: string;
  technicalLabel: string;
}>();
const descriptionLines = computed(() =>
  (props.description || "")
    .split("\n")
    .filter(Boolean)
    .map((line) => {
      const separator = line.indexOf(":");
      return separator > 0
        ? {
            label: line.slice(0, separator).trim(),
            value: line.slice(separator + 1).trim(),
          }
        : { label: "", value: line };
    })
);
</script>

<style scoped>
.quality-explanation {
  margin-top: 0.75em;
  line-height: 1.5;
  overflow-wrap: anywhere;
}

.quality-technical {
  white-space: pre-line;
}

.quality-description {
  display: grid;
  gap: 0.4em;
}
.quality-fact {
  display: grid;
  grid-template-columns: 7em minmax(0, 1fr);
  column-gap: 0.75em;
}
.quality-fact-plain {
  display: block;
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

<template>
  <div class="compliance-settings">
    <h1>Compliance Settings</h1>
    <p>View data processing records and compliance information.</p>
    
    <div class="section">
      <h2>Data Processing Records</h2>
      <button @click="fetchProcessingRecords">View Records</button>
      <ul v-if="processingRecords.length">
        <li v-for="record in processingRecords" :key="record.id">
          {{ record.processing_purpose }} - {{ record.legal_basis }}
        </li>
      </ul>
    </div>
  </div>

<script>
import axios from 'axios'

export default {
  data() {
    return {
      processingRecords: []
    }
  },
  methods: {
    async fetchProcessingRecords() {
      try {
        const firmID = this.$route.params.firmID
        const response = await axios.get(`/api/firms/${firmID}/data-processing-records`)
        this.processingRecords = response.data
      } catch (error) {
        console.error('Error fetching processing records:', error)
      }
    }
  }
}
</script>

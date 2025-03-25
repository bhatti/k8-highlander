// Dashboard JavaScript
class StatefulDashboard {
    constructor() {
        this.updateInterval = 5000; // 5 seconds
        this.lastUpdateTime = null;
        this.isUpdating = false;
        this.errorCount = 0;
        this.connectionError = false;

        this.data = {
            status: {},
            workloads: {},
            pods: []
        };

        this.init();
    }

    init() {
        // Initial data fetch
        this.fetchAllData();

        // Set up periodic updates
        setInterval(() => this.fetchAllData(), this.updateInterval);

        // Update "last updated" time display every second
        setInterval(() => this.updateLastUpdateTime(), 1000);
    }

    async fetchAllData() {
        if (this.isUpdating) return;

        this.isUpdating = true;
        this.showLoading(true);

        try {
            // Fetch controller status
            const statusResponse = await fetch('/api/status');
            if (!statusResponse.ok) throw new Error('Failed to fetch status');
            const statusData = await statusResponse.json();
            this.data.status = statusData.data || statusData;

            // Fetch database info
            const dbInfoResponse = await fetch('/api/dbinfo');
            if (dbInfoResponse.ok) {
                const dbInfoData = await dbInfoResponse.json();
                this.data.dbInfo = dbInfoData.data || {};
            }

            // Fetch workloads
            const workloadsResponse = await fetch('/api/workloads');
            if (!workloadsResponse.ok) throw new Error('Failed to fetch workloads');
            const workloadsData = await workloadsResponse.json();
            this.data.workloads = workloadsData.data || workloadsData;

            // Fetch pods
            const podsResponse = await fetch('/api/pods');
            if (!podsResponse.ok) throw new Error('Failed to fetch pods');
            const podsData = await podsResponse.json();
            this.data.pods = podsData.data || [];

            // Store the successful data for future use if connection fails
            this.lastSuccessfulData = JSON.parse(JSON.stringify(this.data));

            // Reset connection error flag
            this.connectionError = false;

            // Hide connection status indicator
            const connectionStatus = document.getElementById('connection-status');
            if (connectionStatus) {
                connectionStatus.classList.add('d-none');
            }

            // Update the UI
            this.updateUI();

            // Reset error count on successful fetch
            this.errorCount = 0;
            this.hideError();

            // Update last update time
            this.lastUpdateTime = new Date();
            this.updateLastUpdateTime();
        } catch (error) {
            console.error('Error fetching data:', error);
            this.errorCount++;
            this.connectionError = true;

            // Show connection status indicator
            const connectionStatus = document.getElementById('connection-status');
            if (connectionStatus) {
                connectionStatus.classList.remove('d-none');
            }

            this.showError(error.message);

            // If we have previous successful data, use it
            if (Object.keys(this.lastSuccessfulData.status).length > 0) {
                // Keep using the last successful data
                this.data = JSON.parse(JSON.stringify(this.lastSuccessfulData));

                // Update UI with connection error state but keep existing data
                this.updateUIWithConnectionError();
            }
        } finally {
            this.isUpdating = false;
            this.showLoading(false);
        }
    }

    updateUIWithConnectionError() {
        // Update leader status to show connection error
        const leaderContainer = document.getElementById('leader-status-content');
        if (leaderContainer) {
            // Keep the existing content but add an error banner
            const errorBanner = document.createElement('div');
            errorBanner.className = 'alert alert-danger mt-2';
            errorBanner.innerHTML = `
                <strong>Connection Error</strong>
                <p>Unable to connect to server. Displaying last known state.</p>
                <p>Last successful update: ${this.lastUpdateTime ? this.lastUpdateTime.toLocaleString() : 'Never'}</p>
            `;

            // Check if error banner already exists
            const existingBanner = leaderContainer.querySelector('.alert-danger');
            if (!existingBanner) {
                leaderContainer.appendChild(errorBanner);
            }
            // Also update controller state indicator if present
            const controllerStateElem = leaderContainer.querySelector('[data-controller-state]');
            if (controllerStateElem) {
                controllerStateElem.className = 'text-danger';
                controllerStateElem.textContent = 'UNKNOWN (Connection Error)';
            }
        }

        // Update cluster status to show connection error
        const clusterContainer = document.getElementById('cluster-status-content');
        if (clusterContainer) {
            // Update health indicators to show unknown state
            const healthIndicators = clusterContainer.querySelectorAll('.status-indicator');
            healthIndicators.forEach(indicator => {
                indicator.className = 'status-indicator status-error';
            });

            // Add text indicating connection error
            const healthTexts = clusterContainer.querySelectorAll('strong');
            healthTexts.forEach(text => {
                if (text.textContent.includes('Cluster Health:') ||
                    text.textContent.includes('Ready For Traffic:')) {
                    const statusSpan = text.nextElementSibling;
                    if (statusSpan && statusSpan.tagName === 'SPAN') {
                        const textNode = statusSpan.nextSibling;
                        if (textNode) {
                            textNode.textContent = ' Unknown (Connection Error)';
                        }
                    }
                }
            });
        }

        // Update storage status to show connection error
        const storageContainer = document.getElementById('storage-status-content');
        if (storageContainer) {
            const redisIndicator = storageContainer.querySelector('.status-indicator');
            if (redisIndicator) {
                redisIndicator.className = 'status-indicator status-error';
                const textNode = redisIndicator.nextSibling;
                if (textNode) {
                    textNode.textContent = ' Unknown (Connection Error)';
                }
            }
        }

        // Update workload statuses to show connection error
        const workloadContainers = [
            'all-workloads-content',
            'processes-content',
            'cronjobs-content',
            'services-content',
            'persistent-content'
        ];

        workloadContainers.forEach(containerId => {
            const container = document.getElementById(containerId);
            if (container) {
                // Add an error notice at the top if not already present
                const existingNotice = container.querySelector('.connection-error-notice');
                if (!existingNotice) {
                    const notice = document.createElement('div');
                    notice.className = 'alert alert-warning connection-error-notice mb-3';
                    notice.innerHTML = '<strong>Connection Error</strong> - Displaying last known workload state';
                    container.insertBefore(notice, container.firstChild);
                }

                // Update status indicators to show unknown state
                const statusIndicators = container.querySelectorAll('.status-indicator');
                statusIndicators.forEach(indicator => {
                    indicator.className = 'status-indicator status-error';
                });
            }
        });
    }

    updateUI() {
        // If we have a connection error, use the updateUIWithConnectionError method
        if (this.connectionError) {
            this.updateUIWithConnectionError();
            return;
        }

        // Remove any connection error notices
        document.querySelectorAll('.connection-error-notice').forEach(notice => {
            notice.remove();
        });

        // Normal UI update
        this.updateLeaderStatus();
        this.updateClusterStatus();
        this.updateStorageStatus();
        this.updateWorkloads();
        this.updatePods();
    }


    showError(message) {
        console.error('Error:', message);

        // Show error indicator
        const indicator = document.getElementById('error-indicator');
        if (indicator) {
            indicator.classList.remove('d-none');
            indicator.textContent = `Error (${this.errorCount})`;
        }

        // Show error modal for first error
        if (this.errorCount === 1) {
            const errorMessage = document.getElementById('error-message');
            if (errorMessage) {
                errorMessage.textContent = message;
            }

            const errorModal = new bootstrap.Modal(document.getElementById('errorModal'));
            errorModal.show();
        }
    }

    updateLeaderStatus() {
        const status = this.data.status;
        const container = document.getElementById('leader-status-content');
        if (!container) return;

        const isLeader = status.isLeader;
        const statusClass = isLeader ? 'leader-card' : 'follower-card';
        const statusText = isLeader ? 'Leader' : 'Follower';

        // Update card class
        const card = document.getElementById('leader-status-card');
        if (card) {
            card.className = 'card ' + statusClass;
        }

        // Format leader since time
        let leaderSince = 'N/A';
        if (status.leaderSince) {
            leaderSince = new Date(status.leaderSince).toLocaleString();
        }

        // Get controller state (new)
        const controllerState = status.controllerState || 'NORMAL';
        let stateClass = 'text-success';
        let stateAlert = '';

        // Add visual indicators based on controller state (new)
        if (controllerState === 'DEGRADED_DB') {
            stateClass = 'text-warning';
            stateAlert = `
            <div class="alert alert-warning mt-2">
                <strong>Warning:</strong> Database connectivity is degraded.
                <p>The controller is experiencing database connectivity issues.</p>
            </div>
        `;
        } else if (controllerState === 'SPLIT_BRAIN_RISK') {
            stateClass = 'text-danger';
            stateAlert = `
            <div class="alert alert-danger mt-2">
                <strong>Alert:</strong> Split-brain risk detected!
                <p>The controller has lost database connectivity and may be in a split-brain condition.</p>
                ${status.splitBrainProtection ?
                '<p>Split-brain protection is active. Workloads have been stopped for safety.</p>' :
                '<p>Split-brain protection is disabled. This is potentially dangerous.</p>'}
                <p>Manual intervention may be required.</p>
            </div>
        `;
        }

        container.innerHTML = `
        <div class="d-flex align-items-center mb-3">
            <div class="me-3">
                <i class="fas ${isLeader ? 'fa-crown' : 'fa-user'} fa-2x"></i>
            </div>
            <div>
                <h4 class="mb-0">${statusText}</h4>
                <p class="text-muted mb-0">Instance ID: ${status.leaderID || 'Unknown'}</p>
            </div>
        </div>
        <div class="mb-2">
            <strong>Current Leader:</strong> ${status.currentLeader || 'None'}
        </div>
        <div class="mb-2">
            <strong>Last Leader:</strong> ${status.lastLeader || 'None'}
        </div>
        <div class="mb-2">
            <strong>Leader Since:</strong> ${leaderSince}
        </div>
        <div class="mb-2">
            <strong>Leadership Transitions:</strong> ${status.leadershipTransitions || 0}
        </div>
        <div class="mb-2">
            <strong>Failover Count:</strong> ${status.failoverCount || 0}
        </div>
        <div class="mb-2">
            <strong>Controller State:</strong> <span class="${stateClass}">${controllerState}</span>
        </div>
        <div>
            <strong>Last Transition Reason:</strong> ${status.lastLeadershipChangeReason || 'N/A'}
        </div>
        ${stateAlert}
    `;
    }

    updateClusterStatus() {
        const status = this.data.status;
        const container = document.getElementById('cluster-status-content');
        if (!container) return;

        // Format uptime properly
        let uptimeDisplay = 'N/A';
        if (status.uptime) {
            if (typeof status.uptime === 'number') {
                // If it's a number, format it as a duration
                const seconds = Math.floor(status.uptime / 1000000000); // Convert nanoseconds to seconds
                const minutes = Math.floor(seconds / 60);
                const hours = Math.floor(minutes / 60);
                const days = Math.floor(hours / 24);

                if (days > 0) {
                    uptimeDisplay = `${days}d ${hours % 24}h ${minutes % 60}m`;
                } else if (hours > 0) {
                    uptimeDisplay = `${hours}h ${minutes % 60}m ${seconds % 60}s`;
                } else if (minutes > 0) {
                    uptimeDisplay = `${minutes}m ${seconds % 60}s`;
                } else {
                    uptimeDisplay = `${seconds}s`;
                }
            } else {
                // If it's a string or other type, use it directly
                uptimeDisplay = String(status.uptime);
            }
        }

        // Determine ready for traffic status based on multiple factors
        const isReadyForTraffic = status.readyForTraffic === true ||
            (status.isLeader === true && status.clusterHealth === true);

        container.innerHTML = `
<div class="mb-2">
    <strong>Cluster Name:</strong> ${status.clusterName || 'Unknown'}
</div>
<div class="mb-2">
    <strong>Cluster Health:</strong>
    <span class="status-indicator ${status.clusterHealth ? 'status-active' : 'status-inactive'}"></span>
    ${status.clusterHealth ? 'Healthy' : 'Unhealthy'}
</div>
<div class="mb-2">
    <strong>Ready For Traffic:</strong>
    <span class="status-indicator ${isReadyForTraffic ? 'status-active' : 'status-inactive'}"></span>
    ${isReadyForTraffic ? 'Yes' : 'No'}
</div>
<div class="mb-2">
    <strong>Uptime:</strong> ${uptimeDisplay}
</div>
<div class="mb-2">
    <strong>Version:</strong> ${status.version || 'Unknown'}
</div>
<div>
    <strong>Build Info:</strong> ${status.buildInfo || 'Unknown'}
</div>
    `;
    }

    updateStorageStatus() {
        const status = this.data.status;
        const dbInfo = this.data.dbInfo || {};
        const container = document.getElementById('storage-status-content');
        if (!container) return;

        // Determine storage type and connection status
        const storageType = dbInfo.type || status.storageType || 'redis';
        const isConnected = dbInfo.connected || status.dbConnected || false;

        // Format storage type for display
        let storageTypeDisplay = 'Database';
        if (storageType === 'redis') {
            storageTypeDisplay = 'Redis';
        } else if (storageType === 'db') {
            storageTypeDisplay = dbInfo.details?.type || 'Database';
        }

        // Get address
        const address = dbInfo.address || 'Unknown';

        // Get DB failure info (new)
        const dbFailureCount = status.dbFailureCount || 0;
        const dbFailureDuration = status.dbFailureDuration ? this.formatDuration(status.dbFailureDuration) : 'N/A';
        const dbFailureThreshold = status.dbFailureThreshold ? this.formatDuration(status.dbFailureThreshold) : 'N/A';

        // Determine DB health status class (new)
        let dbHealthClass = 'text-success';
        let dbHealthAlert = '';

        if (dbFailureCount > 0) {
            dbHealthClass = 'text-warning';

            if (status.controllerState === 'SPLIT_BRAIN_RISK') {
                dbHealthClass = 'text-danger';
                dbHealthAlert = `
                <div class="alert alert-danger mt-2">
                    <strong>Critical:</strong> Prolonged database connection failure
                    <p>Duration: ${dbFailureDuration}</p>
                    <p>Threshold: ${dbFailureThreshold}</p>
                </div>
            `;
            } else if (status.controllerState === 'DEGRADED_DB') {
                dbHealthAlert = `
                <div class="alert alert-warning mt-2">
                    <strong>Warning:</strong> Database connectivity is degraded
                    <p>Consecutive failures: ${dbFailureCount}</p>
                </div>
            `;
            }
        }

        // Build HTML
        let html = `
        <div class="mb-2">
            <strong>${storageTypeDisplay} Connected:</strong>
            <span class="status-indicator ${isConnected ? 'status-active' : 'status-inactive'}"></span>
            ${isConnected ? 'Connected' : 'Disconnected'}
        </div>
        <div class="mb-2">
            <strong>Address:</strong> ${address}
        </div>
        <div class="mb-2">
            <strong>DB Failure Count:</strong> <span class="${dbHealthClass}">${dbFailureCount}</span>
        </div>`;

        if (dbFailureCount > 0) {
            html += `
        <div class="mb-2">
            <strong>Failure Duration:</strong> ${dbFailureDuration}
        </div>
        <div class="mb-2">
            <strong>Failure Threshold:</strong> ${dbFailureThreshold}
        </div>`;
        }

        // Add any additional details
        if (dbInfo.details && Object.keys(dbInfo.details).length > 0) {
            for (const [key, value] of Object.entries(dbInfo.details)) {
                if (key !== 'type') { // Skip type as we already displayed it
                    html += `
                <div class="mb-2">
                    <strong>${key.charAt(0).toUpperCase() + key.slice(1)}:</strong> ${value || 'N/A'}
                </div>`;
                }
            }
        }

        html += `
        <div class="mb-2">
            <strong>Last Error:</strong> ${status.lastError || 'None'}
        </div>
        <div>
            <a href="/metrics" target="_blank" class="btn btn-sm btn-primary">View Metrics</a>
        </div>
        ${dbHealthAlert}`;

        container.innerHTML = html;
    }

    updateWorkloads() {
        const workloads = this.data.workloads;

        if (!workloads) {
            console.warn("No workloads data available");

            // Update all tabs with "no data" message
            const containers = [
                'all-workloads-content',
                'processes-content',
                'cronjobs-content',
                'services-content',
                'persistent-content'
            ];

            containers.forEach(id => {
                const container = document.getElementById(id);
                if (container) {
                    container.innerHTML = '<div class="alert alert-info">No workload data available.</div>';
                }
            });

            return;
        }

        // Update all workloads tab
        this.updateAllWorkloadsTab(workloads);

        // Debug log all workload types
        console.log("Available workload types:", Object.keys(workloads));

        // Map tab IDs to workload types
        const tabMappings = [
            { tabId: 'processes-content', type: 'processes', dataKey: 'processes', detailsKey: 'processes' },
            { tabId: 'cronjobs-content', type: 'cronJobs', dataKey: 'cronjobs', detailsKey: 'cronJobs' },
            { tabId: 'services-content', type: 'services', dataKey: 'service', detailsKey: 'deployments' },
            { tabId: 'persistent-content', type: 'persistentSets', dataKey: 'persistent', detailsKey: 'persistentSets' }
        ];

        // Update each tab
        for (const mapping of tabMappings) {
            this.updateWorkloadTab(mapping.tabId, workloads, mapping.dataKey, mapping.detailsKey, mapping.type);
        }
    }


    updateWorkloads() {
        const workloads = this.data.workloads;

        if (!workloads) {
            console.warn("No workloads data available");

            // Update all tabs with "no data" message
            const containers = [
                'all-workloads-content',
                'processes-content',
                'cronjobs-content',
                'services-content',
                'persistent-content'
            ];

            containers.forEach(id => {
                const container = document.getElementById(id);
                if (container) {
                    container.innerHTML = '<div class="alert alert-info">No workload data available.</div>';
                }
            });

            return;
        }

        // Update all workloads tab
        this.updateAllWorkloadsTab(workloads);

        // Debug log all workload types
        console.log("Available workload types:", Object.keys(workloads));

        // Map tab IDs to workload types
        const tabMappings = [
            { tabId: 'processes-content', type: 'processes', dataKey: 'processes', detailsKey: 'processes' },
            { tabId: 'cronjobs-content', type: 'cronJobs', dataKey: 'cronjobs', detailsKey: 'cronJobs' },
            { tabId: 'services-content', type: 'services', dataKey: 'service', detailsKey: 'deployments' },
            { tabId: 'persistent-content', type: 'persistentSets', dataKey: 'persistent', detailsKey: 'persistentSets' }
        ];

        // Update each tab
        for (const mapping of tabMappings) {
            this.updateWorkloadTab(mapping.tabId, workloads, mapping.dataKey, mapping.detailsKey, mapping.type);
        }
    }

    updateWorkloadTab(tabId, workloads, dataKey, detailsKey, type) {
        const container = document.getElementById(tabId);
        if (!container) {
            console.warn(`Container not found: ${tabId}`);
            return;
        }

        console.log(`Updating tab ${tabId} with data from ${dataKey}.details.${detailsKey}`);

        // Get the workload data
        const workloadData = workloads ? workloads[dataKey] : null;

        // Check if workload data exists
        if (!workloadData || !workloadData.details) {
            console.warn(`No workload data for ${dataKey}`);
            container.innerHTML = `<div class="alert alert-info">No workloads found.</div>`;
            return;
        }

        // Get the workload items
        const workloadItems = workloadData.details[detailsKey];

        // Check if there are any workload items
        if (!workloadItems || Object.keys(workloadItems).length === 0) {
            console.warn(`No workload items for ${dataKey}.details.${detailsKey}`);
            container.innerHTML = `<div class="alert alert-info">No workloads found.</div>`;
            return;
        }

        console.log(`Found ${Object.keys(workloadItems).length} workload items for ${tabId}`);

        // Build the table
        let html = '<div class="table-responsive"><table class="table table-striped table-hover workload-table">';
        html += '<thead><tr><th>Name</th><th>Status</th><th>Health</th><th>Leader ID</th><th>Cluster</th><th>Details</th></tr></thead><tbody>';

        // Render each workload item
        for (const [name, status] of Object.entries(workloadItems)) {
            html += this.renderWorkloadRow(type, name, status, false, this.data.status);
        }

        html += '</tbody></table></div>';
        container.innerHTML = html;
    }

    updateAllWorkloadsTab(workloads) {
        const container = document.getElementById('all-workloads-content');
        if (!container) return;

        if (!workloads || Object.keys(workloads).length === 0) {
            container.innerHTML = '<div class="alert alert-info">No workloads found.</div>';
            return;
        }

        let html = '<div class="table-responsive"><table class="table table-striped table-hover workload-table">';
        html += '<thead><tr><th>Type</th><th>Name</th><th>Status</th><th>Health</th><th>Leader ID</th><th>Cluster</th><th>Details</th></tr></thead><tbody>';

        // Map of workload types to their details keys
        const typeDetailsMap = {
            'processes': 'processes',
            'cronjobs': 'cronJobs',
            'service': 'deployments',
            'persistent': 'persistentSets'
        };

        let rowCount = 0;

        // Process each workload type
        for (const [type, workloadData] of Object.entries(workloads)) {
            if (!workloadData || !workloadData.details) continue;

            const detailsKey = typeDetailsMap[type];
            if (!detailsKey || !workloadData.details[detailsKey]) continue;

            const workloadItems = workloadData.details[detailsKey];

            // Render each workload item
            for (const [name, status] of Object.entries(workloadItems)) {
                html += this.renderWorkloadRow(type, name, status, true, this.data.status);
                rowCount++;
            }
        }

        html += '</tbody></table></div>';

        // Check if we actually added any rows
        if (rowCount === 0) {
            container.innerHTML = '<div class="alert alert-info">No workloads found.</div>';
        } else {
            container.innerHTML = html;
        }
    }

    updateWorkloadTypeTab(type, workloads) {
        // First, determine the correct container ID based on the type
        let containerId;
        switch(type) {
            case 'processes':
                containerId = 'processes-content';
                break;
            case 'cronJobs':
                containerId = 'cronjobs-content';
                break;
            case 'services':
                containerId = 'services-content';
                break;
            case 'persistentSets':
                containerId = 'persistent-content';
                break;
            default:
                containerId = `${type}-content`;
        }

        const container = document.getElementById(containerId);
        if (!container) {
            console.warn(`Container not found for workload type: ${type}, container ID: ${containerId}`);
            return;
        }

        // Map frontend tab types to backend data structure
        const typeMapping = {
            'processes': {
                dataKey: 'processes',
                detailsKey: 'processes'
            },
            'cronJobs': {
                dataKey: 'cronjobs',
                detailsKey: 'cronJobs'
            },
            'services': {
                dataKey: 'service',
                detailsKey: 'deployments'
            },
            'persistentSets': {
                dataKey: 'persistent',
                detailsKey: 'persistentSets'
            }
        };

        // Get the correct mapping for this type
        const mapping = typeMapping[type];
        if (!mapping) {
            console.warn(`Unknown workload type: ${type}`);
            container.innerHTML = `<div class="alert alert-warning">Unknown workload type: ${type}</div>`;
            return;
        }

        // Get the workload data using the mapping
        const workloadData = workloads ? workloads[mapping.dataKey] : null;

        console.log(`${type} workloads:`, workloadData);

        // Check if workload data exists and has the expected structure
        if (!workloadData || !workloadData.details) {
            console.warn(`No workload data or details for type: ${type}, dataKey: ${mapping.dataKey}`);
            container.innerHTML = `<div class="alert alert-info">No ${type} workloads found.</div>`;
            return;
        }

        // Get the actual workload items from the nested structure
        const workloadItems = workloadData.details[mapping.detailsKey];

        // Check if there are any workload items
        if (!workloadItems || Object.keys(workloadItems).length === 0) {
            console.warn(`No workload items for type: ${type}, detailsKey: ${mapping.detailsKey}`);
            container.innerHTML = `<div class="alert alert-info">No ${type} workloads found.</div>`;
            return;
        }

        console.log(`Found ${Object.keys(workloadItems).length} workload items for ${type}`);

        let html = '<div class="table-responsive"><table class="table table-striped table-hover workload-table">';
        html += '<thead><tr><th>Name</th><th>Status</th><th>Health</th><th>Leader ID</th><th>Cluster</th><th>Details</th></tr></thead><tbody>';

        // Render each workload item
        for (const [name, status] of Object.entries(workloadItems)) {
            html += this.renderWorkloadRow(type, name, status, false, this.data.status);
        }

        html += '</tbody></table></div>';
        container.innerHTML = html;
    }

    renderWorkloadRow(type, name, status, includeType = true, controllerStatus) {
        if (!status) {
            console.warn(`Missing status for workload ${name} of type ${type}`);
            return '';
        }

        const active = status.active ? 'status-active' : 'status-inactive';
        const healthy = status.healthy ? 'status-active' : 'status-warning';

        // Get leader ID and cluster name from controller status
        const leaderId = controllerStatus ? controllerStatus.leaderID || 'Unknown' : 'Unknown';
        const clusterName = controllerStatus ? controllerStatus.clusterName || 'Unknown' : 'Unknown';

        let details = '';
        if (status.details) {
            details = Object.entries(status.details)
                .map(([key, value]) => {
                    // Format the value based on its type
                    let formattedValue = value;
                    if (value === null) {
                        formattedValue = 'null';
                    } else if (typeof value === 'object' && value !== null) {
                        try {
                            formattedValue = JSON.stringify(value);
                        } catch (e) {
                            formattedValue = '[Complex Object]';
                        }
                    }
                    return `<strong>${key}:</strong> ${formattedValue}`;
                })
                .join('<br>');
        }

        let row = '<tr>';
        if (includeType) {
            row += `<td>${type}</td>`;
        }
        row += `
<td>${name}</td>
<td><span class="status-indicator ${active}"></span>${status.active ? 'Active' : 'Inactive'}</td>
<td><span class="status-indicator ${healthy}"></span>${status.healthy ? 'Healthy' : 'Unhealthy'}</td>
<td>${leaderId}</td>
<td>${clusterName}</td>
<td>${details || 'No details available'}</td>
</tr>`;

        return row;
    }

    updatePods() {
        const pods = this.data.pods;
        const container = document.getElementById('pods-content');
        if (!container) return;

        if (!pods || pods.length === 0) {
            container.innerHTML = '<div class="alert alert-info">No pods found or pod information not available.</div>';
            return;
        }

        let html = '<div class="table-responsive"><table class="table table-striped table-hover">';
        html += '<thead><tr><th>Name</th><th>Namespace</th><th>Status</th><th>Node</th><th>Controller ID</th><th>Cluster</th><th>Age</th></tr></thead><tbody>';

        for (const pod of pods) {
            // Determine status class and display text
            let statusClass, statusText;

            switch(pod.status) {
                case 'Running':
                    statusClass = 'status-active';
                    statusText = 'Running';
                    break;
                case 'Succeeded':
                    statusClass = 'status-completed'; // We'll define this new class
                    statusText = 'Completed';
                    break;
                case 'Pending':
                    statusClass = 'status-warning';
                    statusText = 'Pending';
                    break;
                case 'Failed':
                    statusClass = 'status-inactive';
                    statusText = 'Failed';
                    break;
                default:
                    statusClass = 'status-inactive';
                    statusText = pod.status;
            }

            // Get leader ID and cluster name, with fallbacks
            const controllerId = pod.controllerId || this.getControllerFromLabelsOrAnnotations(pod) || 'N/A';
            const clusterName = pod.clusterName || this.getClusterNameFromLabelsOrAnnotations(pod) || 'N/A';

            // Store pod data as a data attribute for the modal
            const podData = JSON.stringify(pod).replace(/"/g, '&quot;');

            html += `
        <tr class="pod-row" data-pod='${podData}' style="cursor: pointer;">
            <td>${pod.name}</td>
            <td>${pod.namespace}</td>
            <td><span class="status-indicator ${statusClass}"></span>${statusText}</td>
            <td>${pod.node || 'N/A'}</td>
            <td>${controllerId}</td>
            <td>${clusterName}</td>
            <td>${pod.age || 'N/A'}</td>
        </tr>
    `;
        }

        html += '</tbody></table></div>';
        container.innerHTML = html;

        // Add click handlers to show pod details
        const podRows = container.querySelectorAll('.pod-row');
        podRows.forEach(row => {
            row.addEventListener('click', () => {
                const podData = JSON.parse(row.getAttribute('data-pod'));
                this.showPodDetails(podData);
            });
        });
    }

// Show pod details in modal
    showPodDetails(pod) {
        const modalTitle = document.getElementById('podDetailsModalLabel');
        const modalContent = document.getElementById('pod-details-content');

        if (!modalTitle || !modalContent) return;

        modalTitle.textContent = `Pod: ${pod.name}`;

        let content = `
        <div class="card mb-3">
            <div class="card-header">
                <h5 class="card-title mb-0">Basic Information</h5>
            </div>
            <div class="card-body">
                <div class="row">
                    <div class="col-md-6">
                        <p><strong>Name:</strong> ${pod.name}</p>
                        <p><strong>Namespace:</strong> ${pod.namespace}</p>
                        <p><strong>Status:</strong> ${pod.status}</p>
                        <p><strong>Node:</strong> ${pod.node || 'N/A'}</p>
                    </div>
                    <div class="col-md-6">
                        <p><strong>Leader ID:</strong> ${pod.leaderId || this.getControllerFromLabelsOrAnnotations(pod) || 'N/A'}</p>
                        <p><strong>Cluster Name:</strong> ${pod.clusterName || this.getClusterNameFromLabelsOrAnnotations(pod) || 'N/A'}</p>
                        <p><strong>Age:</strong> ${pod.age || 'N/A'}</p>
                        <p><strong>Created:</strong> ${pod.createdAt ? new Date(pod.createdAt).toLocaleString() : 'N/A'}</p>
                    </div>
                </div>
            </div>
        </div>
    `;

        // Add labels section if available
        if (pod.labels && Object.keys(pod.labels).length > 0) {
            content += `
            <div class="card mb-3">
                <div class="card-header">
                    <h5 class="card-title mb-0">Labels</h5>
                </div>
                <div class="card-body">
                    <table class="table table-sm">
                        <thead>
                            <tr>
                                <th>Key</th>
                                <th>Value</th>
                            </tr>
                        </thead>
                        <tbody>
        `;

            for (const [key, value] of Object.entries(pod.labels)) {
                content += `
                <tr>
                    <td>${key}</td>
                    <td>${value}</td>
                </tr>
            `;
            }

            content += `
                        </tbody>
                    </table>
                </div>
            </div>
        `;
        }

        // Add annotations section if available
        if (pod.annotations && Object.keys(pod.annotations).length > 0) {
            content += `
            <div class="card mb-3">
                <div class="card-header">
                    <h5 class="card-title mb-0">Annotations</h5>
                </div>
                <div class="card-body">
                    <table class="table table-sm">
                        <thead>
                            <tr>
                                <th>Key</th>
                                <th>Value</th>
                            </tr>
                        </thead>
                        <tbody>
        `;

            for (const [key, value] of Object.entries(pod.annotations)) {
                content += `
                <tr>
                    <td>${key}</td>
                    <td>${value}</td>
                </tr>
            `;
            }

            content += `
                        </tbody>
                    </table>
                </div>
            </div>
        `;
        }

        modalContent.innerHTML = content;

        // Show the modal
        const podDetailsModal = new bootstrap.Modal(document.getElementById('podDetailsModal'));
        podDetailsModal.show();
    }

    // Helper methods to extract controller ID and cluster name from labels or annotations
    getControllerFromLabelsOrAnnotations(pod) {
        if (!pod) return null;

        // Check annotations first
        if (pod.annotations) {
            if (pod.annotations['k8-highlander.io/leader-id']) {
                return pod.annotations['k8-highlander.io/leader-id'];
            }
        }

        // Then check labels
        if (pod.labels) {
            if (pod.labels['controller-id']) {
                return pod.labels['controller-id'];
            }
        }

        return null;
    }

    getClusterNameFromLabelsOrAnnotations(pod) {
        if (!pod) return null;

        // Check annotations first
        if (pod.annotations) {
            if (pod.annotations['k8-highlander.io/cluster-name']) {
                return pod.annotations['k8-highlander.io/cluster-name'];
            }
        }

        // Then check labels
        if (pod.labels) {
            if (pod.labels['cluster-name']) {
                return pod.labels['cluster-name'];
            }
        }

        return null;
    }

    // Utility methods
    formatDuration(durationStr) {
        // If duration is provided as nanoseconds in a numeric format
        if (typeof durationStr === 'number') {
            // Assume nanoseconds and convert to milliseconds
            const ms = durationStr / 1000000;
            const seconds = Math.floor(ms / 1000);
            const minutes = Math.floor(seconds / 60);
            const hours = Math.floor(minutes / 60);

            if (hours > 0) {
                return `${hours}h ${minutes % 60}m ${seconds % 60}s`;
            } else if (minutes > 0) {
                return `${minutes}m ${seconds % 60}s`;
            } else {
                return `${seconds}s`;
            }
        }

        // Handle string duration format (e.g. "5m0s")
        if (typeof durationStr === 'string') {
            // Try to parse Go-style duration strings
            const hours = durationStr.match(/(\d+)h/);
            const minutes = durationStr.match(/(\d+)m/);
            const seconds = durationStr.match(/(\d+)s/);

            let result = '';
            if (hours) result += `${hours[1]}h `;
            if (minutes) result += `${minutes[1]}m `;
            if (seconds) result += `${seconds[1]}s`;

            return result.trim() || durationStr;
        }

        return 'N/A';
    }

    showLoading(show) {
        const indicator = document.getElementById('loading-indicator');
        if (indicator) {
            indicator.classList.toggle('d-none', !show);
        }
    }

    hideError() {
        const indicator = document.getElementById('error-indicator');
        if (indicator) {
            indicator.classList.add('d-none');
        }
    }

    updateLastUpdateTime() {
        const element = document.getElementById('last-update');
        if (element && this.lastUpdateTime) {
            const timeDiff = Math.round((new Date() - this.lastUpdateTime) / 1000);
            element.textContent = `Last update: ${timeDiff}s ago`;

            // Change color if update is stale
            if (this.connectionError) {
                element.className = 'badge bg-danger';
            } else if (timeDiff > 30) {
                element.className = 'badge bg-warning';
            } else {
                element.className = 'badge bg-secondary';
            }
        }
    }
}

// Initialize dashboard when DOM is ready
document.addEventListener('DOMContentLoaded', () => {
    window.dashboard = new StatefulDashboard();
});
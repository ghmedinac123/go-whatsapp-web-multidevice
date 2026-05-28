export default {
    name: 'WebhookConfig',
    props: ['connected'],
    data() {
        return {
            configs: [],
            loading: false,
            saving: false,
            editing: false,
            form: this.emptyForm(),
        }
    },
    computed: {
        deviceOptions() {
            if (!this.connected || this.connected.length === 0) return [];
            return this.connected
                .map(d => d.jid || d.device || d.id)
                .filter(Boolean);
        }
    },
    methods: {
        emptyForm() {
            return {
                id: 0,
                device_id: '',
                webhook_url: '',
                secret: '',
                events_text: '', // comma-separated; empty = all events
                headers_text: '', // JSON object; empty = none
                enabled: true,
            };
        },
        async openModal() {
            try {
                await this.fetchConfigs();
                this.resetForm();
                $('#modalWebhookConfig').modal({observeChanges: true}).modal('show');
            } catch (err) {
                showErrorInfo(this.errMsg(err));
            }
        },
        resetForm() {
            this.editing = false;
            this.form = this.emptyForm();
            // Happy path: pre-select the only connected device.
            if (this.deviceOptions.length === 1) {
                this.form.device_id = this.deviceOptions[0];
            }
        },
        async fetchConfigs() {
            this.loading = true;
            try {
                const response = await window.http.get(`/webhook/configs`);
                this.configs = response.data.results || [];
            } finally {
                this.loading = false;
            }
        },
        eventsLabel(cfg) {
            return (cfg.events && cfg.events.length) ? cfg.events.join(', ') : 'all';
        },
        editConfig(cfg) {
            this.editing = true;
            this.form = {
                id: cfg.id,
                device_id: cfg.device_id,
                webhook_url: cfg.webhook_url,
                secret: cfg.secret || '',
                events_text: (cfg.events || []).join(', '),
                headers_text: cfg.headers ? JSON.stringify(cfg.headers, null, 2) : '',
                enabled: !!cfg.enabled,
            };
        },
        buildPayload() {
            const events = this.form.events_text
                .split(',')
                .map(s => s.trim())
                .filter(Boolean);

            let headers = {};
            const raw = (this.form.headers_text || '').trim();
            if (raw) {
                headers = JSON.parse(raw); // may throw; caught by caller
                if (typeof headers !== 'object' || Array.isArray(headers)) {
                    throw new Error('headers must be a JSON object');
                }
            }

            const payload = {
                webhook_url: this.form.webhook_url,
                secret: this.form.secret,
                events,
                headers,
                enabled: !!this.form.enabled,
            };
            // device_id is optional on create; the backend auto-selects the
            // single connected device when omitted.
            if (this.form.device_id) {
                payload.device_id = this.form.device_id;
            }
            return payload;
        },
        async saveConfig() {
            if (!this.form.webhook_url) {
                showErrorInfo('webhook_url is required');
                return;
            }
            let payload;
            try {
                payload = this.buildPayload();
            } catch (err) {
                showErrorInfo('Invalid headers JSON: ' + this.errMsg(err));
                return;
            }
            this.saving = true;
            try {
                if (this.editing) {
                    await window.http.put(`/webhook/configs/${this.form.id}`, payload);
                    showSuccessInfo('Webhook updated');
                } else {
                    await window.http.post(`/webhook/configs`, payload);
                    showSuccessInfo('Webhook created');
                }
                await this.fetchConfigs();
                this.resetForm();
            } catch (err) {
                showErrorInfo(this.errMsg(err));
            } finally {
                this.saving = false;
            }
        },
        async toggleEnabled(cfg) {
            try {
                const payload = {
                    device_id: cfg.device_id,
                    webhook_url: cfg.webhook_url,
                    secret: cfg.secret || '',
                    events: cfg.events || [],
                    headers: cfg.headers || {},
                    enabled: !cfg.enabled,
                };
                await window.http.put(`/webhook/configs/${cfg.id}`, payload);
                await this.fetchConfigs();
                showSuccessInfo(payload.enabled ? 'Webhook enabled' : 'Webhook disabled');
            } catch (err) {
                showErrorInfo(this.errMsg(err));
            }
        },
        async deleteConfig(cfg) {
            if (!confirm(`Delete webhook ${cfg.webhook_url} for ${cfg.device_id}?`)) return;
            try {
                await window.http.delete(`/webhook/configs/${cfg.id}`);
                await this.fetchConfigs();
                if (this.editing && this.form.id === cfg.id) {
                    this.resetForm();
                }
                showSuccessInfo('Webhook deleted');
            } catch (err) {
                showErrorInfo(this.errMsg(err));
            }
        },
        errMsg(err) {
            if (err && err.response && err.response.data && err.response.data.message) {
                return err.response.data.message;
            }
            return err && err.message ? err.message : String(err);
        },
    },
    template: `
    <div class="olive card" @click="openModal" style="cursor: pointer">
        <div class="content">
            <a class="ui olive right ribbon label">Webhooks</a>
            <div class="header">Device Webhooks</div>
            <div class="description">
                Per-device webhook URLs, event filters and HMAC secrets
            </div>
        </div>
    </div>

    <div class="ui large modal" id="modalWebhookConfig">
        <i class="close icon"></i>
        <div class="header">Per-Device Webhooks</div>
        <div class="content">
            <div v-if="loading" class="ui active centered inline loader"></div>

            <table v-else class="ui celled compact table">
                <thead>
                    <tr>
                        <th>Device (JID)</th>
                        <th>Webhook URL</th>
                        <th>Events</th>
                        <th>Enabled</th>
                        <th>Action</th>
                    </tr>
                </thead>
                <tbody>
                    <tr v-if="configs.length === 0">
                        <td colspan="5" style="text-align:center">No webhooks yet (devices fall back to the global WHATSAPP_WEBHOOK)</td>
                    </tr>
                    <tr v-for="cfg in configs" :key="cfg.id">
                        <td>{{ cfg.device_id }}</td>
                        <td style="word-break:break-all">{{ cfg.webhook_url }}</td>
                        <td>{{ eventsLabel(cfg) }}</td>
                        <td>
                            <span :class="['ui tiny label', cfg.enabled ? 'green' : 'grey']">
                                {{ cfg.enabled ? 'on' : 'off' }}
                            </span>
                        </td>
                        <td>
                            <div style="display:flex; gap:6px;">
                                <button class="ui blue tiny button" @click="editConfig(cfg)">Edit</button>
                                <button class="ui tiny button" @click="toggleEnabled(cfg)">
                                    {{ cfg.enabled ? 'Disable' : 'Enable' }}
                                </button>
                                <button class="ui red tiny button" @click="deleteConfig(cfg)">Delete</button>
                            </div>
                        </td>
                    </tr>
                </tbody>
            </table>

            <div class="ui horizontal divider">{{ editing ? 'Edit webhook' : 'Add webhook' }}</div>

            <form class="ui form" @submit.prevent="saveConfig">
                <div class="two fields">
                    <div class="field">
                        <label>Device (WhatsApp JID)</label>
                        <select v-model="form.device_id" :disabled="editing">
                            <option value="">(auto-detect single device)</option>
                            <option v-for="jid in deviceOptions" :key="jid" :value="jid">{{ jid }}</option>
                        </select>
                        <div class="ui small message" v-if="!editing && deviceOptions.length === 0" style="margin-top:6px">
                            No connected device listed; the server will auto-select if exactly one is logged in.
                        </div>
                    </div>
                    <div class="field">
                        <label>Webhook URL</label>
                        <input type="text" v-model="form.webhook_url" placeholder="https://backend.example.com/webhook">
                    </div>
                </div>
                <div class="two fields">
                    <div class="field">
                        <label>HMAC Secret</label>
                        <input type="text" v-model="form.secret" placeholder="(optional; falls back to WHATSAPP_WEBHOOK_SECRET)">
                    </div>
                    <div class="field">
                        <label>Events (comma-separated)</label>
                        <input type="text" v-model="form.events_text" placeholder="message,message.ack (empty = all)">
                    </div>
                </div>
                <div class="field">
                    <label>Custom Headers (JSON, optional)</label>
                    <textarea rows="2" v-model="form.headers_text" placeholder='{"X-Custom": "value"}'></textarea>
                </div>
                <div class="field">
                    <div class="ui checkbox">
                        <input type="checkbox" v-model="form.enabled" id="whEnabled">
                        <label for="whEnabled">Enabled</label>
                    </div>
                </div>
                <button class="ui olive button" type="submit" :class="{loading: saving}">
                    {{ editing ? 'Update' : 'Create' }}
                </button>
                <button v-if="editing" class="ui button" type="button" @click="resetForm">Cancel</button>
            </form>
        </div>
    </div>
    `
}

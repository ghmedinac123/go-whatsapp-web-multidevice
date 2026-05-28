export default {
    name: 'CampaignManager',
    props: ['connected'],
    data() {
        return {
            campaigns: [],
            templates: [],
            loading: false,
            saving: false,
            editing: false,
            form: this.emptyForm(),
            // detail of the currently opened campaign
            selected: null,
            recipients: [],
            senderForm: {device_id: '', max_daily: 200},
            importForm: {format: 'json', text: ''},
            importPreview: null,
            templateForm: {name: '', body: '', category: ''},
            preview: '',
            pollTimer: null,
            jsonPlaceholder: '[{"phone":"573166203787","name":"Gerlén","empresa":"Fututel"}]',
            csvPlaceholder: 'phone,name,empresa\n573166203787,Gerlén,Fututel',
        }
    },
    computed: {
        deviceOptions() {
            if (!this.connected || this.connected.length === 0) return [];
            return this.connected.map(d => d.jid || d.device || d.id).filter(Boolean);
        }
    },
    methods: {
        emptyForm() {
            return {id: 0, name: '', template_body: '', template_media: ''};
        },
        async openModal() {
            try {
                await Promise.all([this.fetchCampaigns(), this.fetchTemplates()]);
                this.resetForm();
                $('#modalCampaign').modal({
                    observeChanges: true,
                    onHidden: () => this.stopPolling(),
                }).modal('show');
            } catch (err) {
                showErrorInfo(this.errMsg(err));
            }
        },
        resetForm() {
            this.editing = false;
            this.form = this.emptyForm();
            this.preview = '';
        },
        async fetchCampaigns() {
            this.loading = true;
            try {
                const res = await window.http.get(`/campaigns`);
                this.campaigns = res.data.results || [];
            } finally {
                this.loading = false;
            }
        },
        async fetchTemplates() {
            const res = await window.http.get(`/campaign-templates`);
            this.templates = res.data.results || [];
        },
        statusColor(status) {
            return {
                draft: 'grey', scheduled: 'teal', running: 'green',
                paused: 'yellow', completed: 'blue', cancelled: 'red',
            }[status] || 'grey';
        },
        // --- campaign form ---
        editCampaign(c) {
            this.editing = true;
            this.form = {
                id: c.id, name: c.name,
                template_body: c.template_body,
                template_media: c.template_media || '',
            };
            this.refreshPreview();
        },
        async saveCampaign() {
            if (!this.form.name.trim()) return showErrorInfo('Name is required');
            if (!this.form.template_body.trim()) return showErrorInfo('Template body is required');
            this.saving = true;
            try {
                const payload = {
                    name: this.form.name,
                    template_body: this.form.template_body,
                    template_media: this.form.template_media,
                };
                if (this.editing) {
                    await window.http.put(`/campaigns/${this.form.id}`, payload);
                    showSuccessInfo('Campaign updated');
                } else {
                    await window.http.post(`/campaigns`, payload);
                    showSuccessInfo('Campaign created');
                }
                await this.fetchCampaigns();
                this.resetForm();
            } catch (err) {
                showErrorInfo(this.errMsg(err));
            } finally {
                this.saving = false;
            }
        },
        async deleteCampaign(c) {
            if (!confirm(`Delete campaign "${c.name}"?`)) return;
            try {
                await window.http.delete(`/campaigns/${c.id}`);
                if (this.selected && this.selected.campaign.id === c.id) this.closeDetail();
                await this.fetchCampaigns();
                showSuccessInfo('Campaign deleted');
            } catch (err) {
                showErrorInfo(this.errMsg(err));
            }
        },
        // --- lifecycle ---
        async action(c, verb) {
            try {
                await window.http.post(`/campaigns/${c.id}/${verb}`);
                showSuccessInfo(`Campaign ${verb}`);
                await this.fetchCampaigns();
                if (this.selected && this.selected.campaign.id === c.id) await this.openDetail(c.id);
            } catch (err) {
                showErrorInfo(this.errMsg(err));
            }
        },
        // --- detail / stats ---
        async openDetail(id) {
            const res = await window.http.get(`/campaigns/${id}`);
            this.selected = res.data.results;
            await this.fetchRecipients(id);
            this.startPolling();
        },
        async selectCampaign(c) {
            try {
                await this.openDetail(c.id);
            } catch (err) {
                showErrorInfo(this.errMsg(err));
            }
        },
        closeDetail() {
            this.stopPolling();
            this.selected = null;
            this.recipients = [];
            this.importPreview = null;
        },
        async fetchRecipients(id) {
            const res = await window.http.get(`/campaigns/${id}/recipients?limit=100`);
            this.recipients = res.data.results || [];
        },
        async refreshStats() {
            if (!this.selected) return;
            try {
                const id = this.selected.campaign.id;
                const res = await window.http.get(`/campaigns/${id}/stats`);
                this.selected.stats = res.data.results;
                this.selected.campaign.status = res.data.results.status;
            } catch (_) { /* ignore transient poll errors */ }
        },
        startPolling() {
            this.stopPolling();
            this.pollTimer = setInterval(() => this.refreshStats(), 3000);
        },
        stopPolling() {
            if (this.pollTimer) {
                clearInterval(this.pollTimer);
                this.pollTimer = null;
            }
        },
        progressPct(stats) {
            if (!stats || !stats.total) return 0;
            const done = stats.total - stats.pending;
            return Math.round((done / stats.total) * 100);
        },
        // --- senders ---
        async addSender() {
            if (!this.senderForm.device_id) return showErrorInfo('Select a device');
            try {
                await window.http.post(`/campaigns/${this.selected.campaign.id}/senders`, {
                    device_id: this.senderForm.device_id,
                    max_daily: Number(this.senderForm.max_daily) || 200,
                });
                this.senderForm.device_id = '';
                await this.openDetail(this.selected.campaign.id);
                showSuccessInfo('Sender added');
            } catch (err) {
                showErrorInfo(this.errMsg(err));
            }
        },
        async removeSender(s) {
            try {
                await window.http.delete(`/campaigns/${this.selected.campaign.id}/senders/${s.id}`);
                await this.openDetail(this.selected.campaign.id);
                showSuccessInfo('Sender removed');
            } catch (err) {
                showErrorInfo(this.errMsg(err));
            }
        },
        // --- recipient import ---
        previewImport() {
            this.importPreview = null;
            const text = (this.importForm.text || '').trim();
            if (!text) return showErrorInfo('Nothing to preview');
            try {
                let rows = [];
                if (this.importForm.format === 'json') {
                    const arr = JSON.parse(text);
                    if (!Array.isArray(arr)) throw new Error('JSON must be an array');
                    rows = arr.slice(0, 5);
                } else {
                    rows = text.split(/\r?\n/).filter(Boolean).slice(0, 6).map(line => ({raw: line}));
                }
                this.importPreview = rows;
            } catch (err) {
                showErrorInfo('Preview failed: ' + this.errMsg(err));
            }
        },
        async importRecipients() {
            const id = this.selected.campaign.id;
            const text = (this.importForm.text || '').trim();
            if (!text) return showErrorInfo('Nothing to import');
            try {
                let res;
                if (this.importForm.format === 'json') {
                    const arr = JSON.parse(text);
                    res = await window.http.post(`/campaigns/${id}/recipients`, arr);
                } else {
                    res = await window.http.post(`/campaigns/${id}/recipients?format=csv`, text, {
                        headers: {'Content-Type': 'text/csv'},
                    });
                }
                const r = res.data.results || {};
                showSuccessInfo(`Imported ${r.imported} (skipped ${r.skipped})`);
                this.importForm.text = '';
                this.importPreview = null;
                await this.openDetail(id);
            } catch (err) {
                showErrorInfo(this.errMsg(err));
            }
        },
        // --- templates ---
        async saveTemplate() {
            if (!this.templateForm.name.trim() || !this.templateForm.body.trim()) {
                return showErrorInfo('Template name and body are required');
            }
            try {
                await window.http.post(`/campaign-templates`, this.templateForm);
                this.templateForm = {name: '', body: '', category: ''};
                await this.fetchTemplates();
                showSuccessInfo('Template saved');
            } catch (err) {
                showErrorInfo(this.errMsg(err));
            }
        },
        useTemplate(t) {
            this.form.template_body = t.body;
            if (t.media_url) this.form.template_media = t.media_url;
            this.refreshPreview();
            showSuccessInfo(`Loaded template "${t.name}"`);
        },
        async deleteTemplate(t) {
            if (!confirm(`Delete template "${t.name}"?`)) return;
            try {
                await window.http.delete(`/campaign-templates/${t.id}`);
                await this.fetchTemplates();
                showSuccessInfo('Template deleted');
            } catch (err) {
                showErrorInfo(this.errMsg(err));
            }
        },
        // --- spintax preview (client-side; mirrors the Go engine) ---
        refreshPreview() {
            this.preview = this.resolveSpin(this.form.template_body || '');
        },
        resolveSpin(s) {
            let out = '';
            let i = 0;
            while (i < s.length) {
                if (s[i] !== '{') {
                    out += s[i];
                    i++;
                    continue;
                }
                const end = this.matchBrace(s, i);
                if (end === -1) {
                    out += s[i];
                    i++;
                    continue;
                }
                const inner = s.slice(i + 1, end);
                if (this.hasTopLevelPipe(inner)) {
                    const opts = this.splitTopLevel(inner);
                    out += this.resolveSpin(opts[Math.floor(Math.random() * opts.length)]);
                } else if (inner.includes('{')) {
                    out += this.resolveSpin(inner);
                } else {
                    out += '{' + inner + '}'; // variable placeholder kept literal
                }
                i = end + 1;
            }
            return out;
        },
        matchBrace(s, open) {
            let depth = 0;
            for (let i = open; i < s.length; i++) {
                if (s[i] === '{') depth++;
                else if (s[i] === '}') {
                    depth--;
                    if (depth === 0) return i;
                }
            }
            return -1;
        },
        splitTopLevel(s) {
            const parts = [];
            let depth = 0, start = 0;
            for (let i = 0; i < s.length; i++) {
                if (s[i] === '{') depth++;
                else if (s[i] === '}') { if (depth > 0) depth--; }
                else if (s[i] === '|' && depth === 0) { parts.push(s.slice(start, i)); start = i + 1; }
            }
            parts.push(s.slice(start));
            return parts;
        },
        hasTopLevelPipe(s) {
            let depth = 0;
            for (let i = 0; i < s.length; i++) {
                if (s[i] === '{') depth++;
                else if (s[i] === '}') { if (depth > 0) depth--; }
                else if (s[i] === '|' && depth === 0) return true;
            }
            return false;
        },
        errMsg(err) {
            if (err && err.response && err.response.data && err.response.data.message) {
                return err.response.data.message;
            }
            return err && err.message ? err.message : String(err);
        },
    },
    template: `
    <div class="purple card" @click="openModal" style="cursor: pointer">
        <div class="content">
            <a class="ui purple right ribbon label">Campaigns</a>
            <div class="header">Mass Campaigns</div>
            <div class="description">
                Bulk messaging with spintax, human delays, number rotation & health scoring
            </div>
        </div>
    </div>

    <div class="ui fullscreen modal" id="modalCampaign">
        <i class="close icon"></i>
        <div class="header">Mass Messaging Campaigns</div>
        <div class="content" style="max-height: 80vh; overflow-y: auto;">

            <!-- ===== LIST VIEW ===== -->
            <div v-if="!selected">
                <div v-if="loading" class="ui active centered inline loader"></div>
                <table v-else class="ui celled compact table">
                    <thead>
                        <tr>
                            <th>Name</th><th>Status</th><th>Template</th><th>Actions</th>
                        </tr>
                    </thead>
                    <tbody>
                        <tr v-if="campaigns.length === 0">
                            <td colspan="4" style="text-align:center">No campaigns yet. Create one below.</td>
                        </tr>
                        <tr v-for="c in campaigns" :key="c.id">
                            <td><a @click="selectCampaign(c)" style="cursor:pointer">{{ c.name }}</a></td>
                            <td><span :class="['ui tiny label', statusColor(c.status)]">{{ c.status }}</span></td>
                            <td style="max-width:260px; word-break:break-word">{{ c.template_body }}</td>
                            <td>
                                <div class="ui tiny buttons">
                                    <button class="ui blue button" @click="selectCampaign(c)">Open</button>
                                    <button v-if="c.status==='draft' || c.status==='paused' || c.status==='scheduled'" class="ui green button" @click="action(c, c.status==='paused' ? 'resume' : 'start')">
                                        {{ c.status==='paused' ? 'Resume' : 'Start' }}
                                    </button>
                                    <button v-if="c.status==='running'" class="ui yellow button" @click="action(c,'pause')">Pause</button>
                                    <button v-if="c.status==='running' || c.status==='paused'" class="ui orange button" @click="action(c,'cancel')">Cancel</button>
                                    <button class="ui button" @click="editCampaign(c)">Edit</button>
                                    <button class="ui red button" @click="deleteCampaign(c)">Del</button>
                                </div>
                            </td>
                        </tr>
                    </tbody>
                </table>

                <div class="ui horizontal divider">{{ editing ? 'Edit campaign' : 'New campaign' }}</div>
                <form class="ui form" @submit.prevent="saveCampaign">
                    <div class="two fields">
                        <div class="field">
                            <label>Name</label>
                            <input type="text" v-model="form.name" placeholder="Promo mayo">
                        </div>
                        <div class="field">
                            <label>Media URL (optional)</label>
                            <input type="text" v-model="form.template_media" placeholder="https://.../image.jpg">
                        </div>
                    </div>
                    <div class="field">
                        <label>Template body (spintax + variables)</label>
                        <textarea rows="3" v-model="form.template_body" @input="refreshPreview"
                            placeholder="{Hola|Buenas} {nombre}, {tenemos|traemos} una {oferta|promo} para {empresa}"></textarea>
                    </div>
                    <div class="field" v-if="preview">
                        <label>Live preview</label>
                        <div class="ui message" style="white-space:pre-wrap">{{ preview }}</div>
                        <button class="ui mini button" type="button" @click="refreshPreview">Reroll</button>
                    </div>
                    <button class="ui purple button" type="submit" :class="{loading: saving}">
                        {{ editing ? 'Update' : 'Create' }}
                    </button>
                    <button v-if="editing" class="ui button" type="button" @click="resetForm">Cancel</button>
                </form>

                <div class="ui horizontal divider">Reusable templates</div>
                <div class="ui list">
                    <div class="item" v-for="t in templates" :key="t.id">
                        <div class="right floated content">
                            <button class="ui mini button" @click="useTemplate(t)">Use</button>
                            <button class="ui mini red button" @click="deleteTemplate(t)">Delete</button>
                        </div>
                        <div class="content">
                            <strong>{{ t.name }}</strong>
                            <span v-if="t.category" class="ui tiny label">{{ t.category }}</span>
                            <div class="description" style="word-break:break-word">{{ t.body }}</div>
                        </div>
                    </div>
                </div>
                <form class="ui form" @submit.prevent="saveTemplate" style="margin-top:10px">
                    <div class="three fields">
                        <div class="field"><input type="text" v-model="templateForm.name" placeholder="Template name"></div>
                        <div class="field"><input type="text" v-model="templateForm.category" placeholder="Category (marketing...)"></div>
                        <div class="field"><button class="ui button" type="submit">Save template</button></div>
                    </div>
                    <div class="field">
                        <textarea rows="2" v-model="templateForm.body" placeholder="{Hi|Hey} {nombre}"></textarea>
                    </div>
                </form>
            </div>

            <!-- ===== DETAIL VIEW ===== -->
            <div v-else>
                <button class="ui button" @click="closeDetail"><i class="arrow left icon"></i> Back</button>
                <h3 class="ui header" style="display:inline-block; margin-left:10px">
                    {{ selected.campaign.name }}
                    <span :class="['ui label', statusColor(selected.campaign.status)]">{{ selected.campaign.status }}</span>
                </h3>

                <div class="ui buttons" style="float:right">
                    <button v-if="selected.campaign.status==='draft' || selected.campaign.status==='scheduled'" class="ui green button" @click="action(selected.campaign,'start')">Start</button>
                    <button v-if="selected.campaign.status==='paused'" class="ui green button" @click="action(selected.campaign,'resume')">Resume</button>
                    <button v-if="selected.campaign.status==='running'" class="ui yellow button" @click="action(selected.campaign,'pause')">Pause</button>
                    <button v-if="selected.campaign.status==='running' || selected.campaign.status==='paused'" class="ui orange button" @click="action(selected.campaign,'cancel')">Cancel</button>
                </div>

                <!-- progress + stats -->
                <div class="ui segment" style="margin-top:50px">
                    <div class="ui indicating progress">
                        <div class="bar" :style="{width: progressPct(selected.stats) + '%'}"></div>
                        <div class="label">{{ progressPct(selected.stats) }}% processed</div>
                    </div>
                    <div class="ui tiny statistics" style="margin-top:10px">
                        <div class="statistic"><div class="value">{{ selected.stats.total }}</div><div class="label">Total</div></div>
                        <div class="statistic"><div class="value">{{ selected.stats.pending }}</div><div class="label">Pending</div></div>
                        <div class="green statistic"><div class="value">{{ selected.stats.sent }}</div><div class="label">Sent</div></div>
                        <div class="blue statistic"><div class="value">{{ selected.stats.delivered }}</div><div class="label">Delivered</div></div>
                        <div class="red statistic"><div class="value">{{ selected.stats.failed }}</div><div class="label">Failed</div></div>
                        <div class="teal statistic"><div class="value">{{ selected.stats.replied }}</div><div class="label">Replied</div></div>
                    </div>
                </div>

                <!-- senders -->
                <div class="ui horizontal divider">Sender pool (number rotation)</div>
                <table class="ui celled compact table">
                    <thead><tr><th>Device</th><th>Health</th><th>Sent today</th><th>Max/day</th><th>Enabled</th><th></th></tr></thead>
                    <tbody>
                        <tr v-if="selected.senders.length === 0"><td colspan="6" style="text-align:center">No senders. Add at least one device to run.</td></tr>
                        <tr v-for="s in selected.senders" :key="s.id">
                            <td style="word-break:break-all">{{ s.device_id }}</td>
                            <td>
                                <span :class="['ui tiny label', s.health_score < 0.3 ? 'red' : (s.health_score < 0.7 ? 'yellow' : 'green')]">
                                    {{ (s.health_score).toFixed(2) }}
                                </span>
                            </td>
                            <td>{{ s.sent_today }}</td>
                            <td>{{ s.max_daily }}</td>
                            <td><span :class="['ui tiny label', s.enabled ? 'green' : 'grey']">{{ s.enabled ? 'on' : 'off' }}</span></td>
                            <td><button class="ui mini red button" @click="removeSender(s)">Remove</button></td>
                        </tr>
                    </tbody>
                </table>
                <form class="ui form" @submit.prevent="addSender">
                    <div class="three fields">
                        <div class="field">
                            <select v-model="senderForm.device_id">
                                <option value="">(select connected device)</option>
                                <option v-for="jid in deviceOptions" :key="jid" :value="jid">{{ jid }}</option>
                            </select>
                        </div>
                        <div class="field"><input type="number" v-model="senderForm.max_daily" min="1" placeholder="Max per day (200)"></div>
                        <div class="field"><button class="ui button" type="submit">Add sender</button></div>
                    </div>
                </form>

                <!-- recipient import -->
                <div class="ui horizontal divider">Import recipients</div>
                <form class="ui form">
                    <div class="inline fields">
                        <label>Format</label>
                        <div class="field"><div class="ui radio checkbox"><input type="radio" value="json" v-model="importForm.format"><label>JSON</label></div></div>
                        <div class="field"><div class="ui radio checkbox"><input type="radio" value="csv" v-model="importForm.format"><label>CSV</label></div></div>
                    </div>
                    <div class="field">
                        <textarea rows="4" v-model="importForm.text"
                            :placeholder="importForm.format === 'json' ? jsonPlaceholder : csvPlaceholder"></textarea>
                    </div>
                    <button class="ui button" type="button" @click="previewImport">Preview</button>
                    <button class="ui green button" type="button" @click="importRecipients">Import</button>
                    <div v-if="importPreview" class="ui small message">
                        <div v-for="(row, idx) in importPreview" :key="idx">{{ JSON.stringify(row) }}</div>
                    </div>
                </form>

                <!-- recipient list -->
                <div class="ui horizontal divider">Recipients (first 100)</div>
                <table class="ui celled compact small table">
                    <thead><tr><th>Phone</th><th>Name</th><th>Status</th><th>Sent by</th><th>Error</th></tr></thead>
                    <tbody>
                        <tr v-if="recipients.length === 0"><td colspan="5" style="text-align:center">No recipients yet.</td></tr>
                        <tr v-for="r in recipients" :key="r.id">
                            <td>{{ r.phone }}</td>
                            <td>{{ r.name }}</td>
                            <td><span :class="['ui tiny label', statusColor(r.status === 'sent' ? 'running' : (r.status === 'failed' ? 'cancelled' : 'grey'))]">{{ r.status }}</span></td>
                            <td style="word-break:break-all">{{ r.sent_by_device }}</td>
                            <td style="color:#db2828">{{ r.error_message }}</td>
                        </tr>
                    </tbody>
                </table>
            </div>
        </div>
    </div>
    `
}

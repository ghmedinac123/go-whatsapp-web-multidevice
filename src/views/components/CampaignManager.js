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
            // tag discovery for the template editor
            analyzeResult: null,
            availableTags: [],
            // detail of the currently opened campaign
            selected: null,
            recipients: [],
            senderForm: {device_id: '', max_daily: 200},
            batchSize: 0,
            // recipient import (detail view)
            importMode: 'paste',
            importForm: {format: 'json', text: ''},
            importPreview: null,
            importFile: null,
            fileAnalysis: null,
            selectedPhoneColumn: '',
            savingTemplate: false,
            // contacts picker
            contacts: [],
            contactsLoading: false,
            contactSearch: '',
            selectedContacts: {},
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
        },
        // Tags shown in the template editor: always {nombre}, plus any from the
        // campaign's recipients and the most recent file analysis.
        editorTags() {
            const set = new Set(['{nombre}']);
            (this.availableTags || []).forEach(t => set.add(t));
            if (this.analyzeResult) (this.analyzeResult.tags || []).forEach(t => set.add(t));
            return [...set];
        },
        filteredContacts() {
            const q = this.contactSearch.trim().toLowerCase();
            if (!q) return this.contacts;
            return this.contacts.filter(c =>
                (c.name || '').toLowerCase().includes(q) || this.jidToPhone(c.jid).includes(q));
        },
        selectedContactCount() {
            return Object.values(this.selectedContacts).filter(Boolean).length;
        },
        // Tags usable in the detail-view inline template editor, by import mode.
        detailTags() {
            if (this.importMode === 'contacts') return ['{nombre}', '{phone}'];
            if (this.importMode === 'file' && this.fileAnalysis) {
                return (this.fileAnalysis.columns || [])
                    .filter(c => c && c !== this.selectedPhoneColumn)
                    .map(c => '{' + c + '}');
            }
            return [];
        },
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
            this.analyzeResult = null;
            this.availableTags = [];
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
        async editCampaign(c) {
            this.editing = true;
            this.form = {
                id: c.id, name: c.name,
                template_body: c.template_body,
                template_media: c.template_media || '',
            };
            this.analyzeResult = null;
            this.refreshPreview();
            try {
                const res = await window.http.get(`/campaigns/${c.id}/variables`);
                this.availableTags = (res.data.results && res.data.results.tags) || [];
            } catch (_) {
                this.availableTags = [];
            }
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
        // insert a {tag} into the template body at the end (with a separating space)
        insertTag(tag) {
            const body = this.form.template_body || '';
            this.form.template_body = body + (body && !body.endsWith(' ') ? ' ' : '') + tag;
            this.refreshPreview();
        },
        // analyze a CSV/Excel file just to discover its columns -> tags (no insert)
        async analyzeEditorFile(e) {
            const file = e.target.files && e.target.files[0];
            if (!file) return;
            try {
                const fd = new FormData();
                fd.append('file', file);
                const res = await window.http.post(`/campaigns/import/analyze`, fd);
                this.analyzeResult = res.data.results;
                showSuccessInfo(`Columnas: ${(this.analyzeResult.columns || []).join(', ')}`);
            } catch (err) {
                showErrorInfo(this.errMsg(err));
            } finally {
                e.target.value = '';
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
            this.importFile = null;
            this.contacts = [];
            this.selectedContacts = {};
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
        // --- recipient import: paste ---
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
        // buildRecipientsURL composes the import URL with batch_size + extra params.
        buildRecipientsURL(id, params) {
            const sp = new URLSearchParams();
            const bs = Number(this.batchSize) || 0;
            if (bs > 0) sp.set('batch_size', bs);
            Object.entries(params || {}).forEach(([k, v]) => { if (v) sp.set(k, v); });
            const qs = sp.toString();
            return `/campaigns/${id}/recipients${qs ? ('?' + qs) : ''}`;
        },
        async importPaste() {
            const id = this.selected.campaign.id;
            const text = (this.importForm.text || '').trim();
            if (!text) return showErrorInfo('Nothing to import');
            try {
                let res;
                if (this.importForm.format === 'json') {
                    const arr = JSON.parse(text);
                    res = await window.http.post(this.buildRecipientsURL(id, {}), arr);
                } else {
                    res = await window.http.post(this.buildRecipientsURL(id, {format: 'csv'}), text, {
                        headers: {'Content-Type': 'text/csv'},
                    });
                }
                this.afterImport(res);
                this.importForm.text = '';
                this.importPreview = null;
            } catch (err) {
                showErrorInfo(this.errMsg(err));
            }
        },
        // --- recipient import: file (CSV/Excel) ---
        // Selecting a file immediately analyzes it so the user can pick the phone column.
        async onImportFile(e) {
            this.importFile = (e.target.files && e.target.files[0]) || null;
            this.fileAnalysis = null;
            this.selectedPhoneColumn = '';
            if (!this.importFile) return;
            try {
                const fd = new FormData();
                fd.append('file', this.importFile);
                const res = await window.http.post(`/campaigns/import/analyze`, fd);
                this.fileAnalysis = res.data.results;
                this.selectedPhoneColumn = this.fileAnalysis.phone_column ||
                    (this.fileAnalysis.columns || [])[0] || '';
            } catch (err) {
                showErrorInfo('No se pudo analizar el archivo: ' + this.errMsg(err));
            }
        },
        async importFromFile() {
            if (!this.importFile) return showErrorInfo('Selecciona un archivo CSV/Excel');
            if (!this.selectedPhoneColumn) return showErrorInfo('Selecciona la columna del teléfono');
            const id = this.selected.campaign.id;
            try {
                const fd = new FormData();
                fd.append('file', this.importFile);
                const url = this.buildRecipientsURL(id, {phone_column: this.selectedPhoneColumn});
                const res = await window.http.post(url, fd);
                this.afterImport(res);
                this.importFile = null;
                this.fileAnalysis = null;
                this.selectedPhoneColumn = '';
            } catch (err) {
                showErrorInfo(this.errMsg(err));
            }
        },
        // insert a tag into the open campaign's template (detail view) + persist on save
        insertTagDetail(tag) {
            const body = this.selected.campaign.template_body || '';
            this.selected.campaign.template_body = body + (body && !body.endsWith(' ') ? ' ' : '') + tag;
        },
        async saveDetailTemplate() {
            this.savingTemplate = true;
            try {
                const c = this.selected.campaign;
                await window.http.put(`/campaigns/${c.id}`, {
                    name: c.name,
                    template_body: c.template_body,
                    template_media: c.template_media || '',
                });
                showSuccessInfo('Plantilla guardada');
            } catch (err) {
                showErrorInfo(this.errMsg(err));
            } finally {
                this.savingTemplate = false;
            }
        },
        // --- recipient import: contacts ---
        jidToPhone(jid) {
            return (jid || '').split('@')[0].split(':')[0];
        },
        async fetchContacts() {
            this.contactsLoading = true;
            try {
                const res = await window.http.get(`/user/my/contacts`);
                const results = res.data.results || {};
                const list = results.data || [];
                // keep only entries with a usable phone JID
                this.contacts = list.filter(c => this.jidToPhone(c.jid).length >= 8);
            } catch (err) {
                showErrorInfo('No se pudieron cargar contactos (¿device seleccionado?): ' + this.errMsg(err));
            } finally {
                this.contactsLoading = false;
            }
        },
        toggleContact(jid) {
            this.selectedContacts = {...this.selectedContacts, [jid]: !this.selectedContacts[jid]};
        },
        selectAllContacts() {
            const next = {};
            this.filteredContacts.forEach(c => { next[c.jid] = true; });
            this.selectedContacts = next;
        },
        clearContactSelection() {
            this.selectedContacts = {};
        },
        async importFromContacts() {
            const id = this.selected.campaign.id;
            const chosen = this.contacts
                .filter(c => this.selectedContacts[c.jid])
                .map(c => ({phone: this.jidToPhone(c.jid), name: c.name || ''}));
            if (chosen.length === 0) return showErrorInfo('Selecciona al menos un contacto');
            try {
                const res = await window.http.post(this.buildRecipientsURL(id, {}), chosen);
                this.afterImport(res);
                this.selectedContacts = {};
            } catch (err) {
                showErrorInfo(this.errMsg(err));
            }
        },
        afterImport(res) {
            const r = (res && res.data && res.data.results) || {};
            showSuccessInfo(`Importados ${r.imported} (omitidos ${r.skipped})`);
            this.openDetail(this.selected.campaign.id);
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
                        <tr><th>Name</th><th>Status</th><th>Template</th><th>Actions</th></tr>
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

                    <div class="field">
                        <label>Tags disponibles (clic para insertar)</label>
                        <div>
                            <a v-for="tag in editorTags" :key="tag" class="ui small teal label" style="cursor:pointer; margin-bottom:4px" @click="insertTag(tag)">{{ tag }}</a>
                        </div>
                        <div style="margin-top:6px">
                            <label class="ui small button" style="cursor:pointer">
                                <i class="upload icon"></i> Analizar CSV/Excel para detectar columnas
                                <input type="file" accept=".csv,.xlsx,.json" hidden @change="analyzeEditorFile">
                            </label>
                        </div>
                        <div v-if="analyzeResult" class="ui small message">
                            <strong>Columnas detectadas:</strong> {{ (analyzeResult.columns || []).join(', ') }}
                            <span v-if="analyzeResult.phone_column"> · teléfono: <code>{{ analyzeResult.phone_column }}</code></span>
                            <div v-if="(analyzeResult.sample_rows||[]).length" style="margin-top:4px; font-size:0.85em; color:#666">
                                Ejemplo: {{ JSON.stringify(analyzeResult.sample_rows[0]) }}
                            </div>
                        </div>
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
                <div class="ui secondary menu">
                    <a class="item" :class="{active: importMode==='paste'}" @click="importMode='paste'">Pegar</a>
                    <a class="item" :class="{active: importMode==='file'}" @click="importMode='file'">Archivo (CSV/Excel)</a>
                    <a class="item" :class="{active: importMode==='contacts'}" @click="importMode='contacts'; contacts.length || fetchContacts()">Contactos</a>
                </div>

                <div class="ui form" style="margin-bottom:10px">
                    <div class="inline field">
                        <label>Tamaño de lote (N)</label>
                        <input type="number" v-model="batchSize" min="0" style="width:120px" placeholder="0 = sin lotes">
                        <span style="color:#888; margin-left:8px">0 = un solo lote; N = tandas de N</span>
                    </div>
                </div>

                <!-- paste mode -->
                <form v-if="importMode==='paste'" class="ui form">
                    <div class="inline fields">
                        <label>Formato</label>
                        <div class="field"><div class="ui radio checkbox"><input type="radio" value="json" v-model="importForm.format"><label>JSON</label></div></div>
                        <div class="field"><div class="ui radio checkbox"><input type="radio" value="csv" v-model="importForm.format"><label>CSV</label></div></div>
                    </div>
                    <div class="field">
                        <textarea rows="4" v-model="importForm.text" :placeholder="importForm.format === 'json' ? jsonPlaceholder : csvPlaceholder"></textarea>
                    </div>
                    <button class="ui button" type="button" @click="previewImport">Preview</button>
                    <button class="ui green button" type="button" @click="importPaste">Import</button>
                    <div v-if="importPreview" class="ui small message">
                        <div v-for="(row, idx) in importPreview" :key="idx">{{ JSON.stringify(row) }}</div>
                    </div>
                </form>

                <!-- file mode -->
                <form v-else-if="importMode==='file'" class="ui form">
                    <div class="field">
                        <label>Archivo CSV o Excel (.xlsx)</label>
                        <input type="file" accept=".csv,.xlsx,.json" @change="onImportFile">
                    </div>
                    <div v-if="fileAnalysis">
                        <div class="two fields">
                            <div class="field">
                                <label>Columna del teléfono (obligatorio)</label>
                                <select v-model="selectedPhoneColumn">
                                    <option value="">(selecciona la columna)</option>
                                    <option v-for="col in fileAnalysis.columns" :key="col" :value="col">{{ col }}</option>
                                </select>
                            </div>
                            <div class="field">
                                <label>Filas detectadas</label>
                                <input type="text" :value="fileAnalysis.row_count + ' filas'" disabled>
                            </div>
                        </div>
                        <div v-if="(fileAnalysis.sample_rows||[]).length" class="ui small message" style="font-size:0.85em">
                            Ejemplo: {{ JSON.stringify(fileAnalysis.sample_rows[0]) }}
                        </div>
                    </div>
                    <button class="ui green button" type="button" @click="importFromFile" :disabled="!selectedPhoneColumn">Importar archivo</button>
                    <span v-if="importFile" style="margin-left:8px; color:#666">{{ importFile.name }}</span>
                </form>

                <!-- contacts mode -->
                <div v-else>
                    <div class="ui action input" style="margin-bottom:8px; width:100%">
                        <input type="text" v-model="contactSearch" placeholder="Buscar contacto por nombre o número...">
                        <button class="ui button" @click="selectAllContacts">Seleccionar visibles</button>
                        <button class="ui button" @click="clearContactSelection">Limpiar</button>
                    </div>
                    <div v-if="contactsLoading" class="ui active centered inline loader"></div>
                    <div v-else style="max-height:220px; overflow-y:auto; border:1px solid #eee; border-radius:4px">
                        <table class="ui very compact small table" style="margin:0">
                            <tbody>
                                <tr v-if="filteredContacts.length === 0"><td style="text-align:center; color:#888">Sin contactos (selecciona un device y reintenta)</td></tr>
                                <tr v-for="ct in filteredContacts" :key="ct.jid" @click="toggleContact(ct.jid)" style="cursor:pointer">
                                    <td style="width:36px">
                                        <div class="ui checkbox"><input type="checkbox" :checked="!!selectedContacts[ct.jid]"></div>
                                    </td>
                                    <td>{{ ct.name || '(sin nombre)' }}</td>
                                    <td style="color:#888">{{ jidToPhone(ct.jid) }}</td>
                                </tr>
                            </tbody>
                        </table>
                    </div>
                    <div style="margin-top:8px">
                        <button class="ui green button" @click="importFromContacts">Importar {{ selectedContactCount }} seleccionados</button>
                        <button class="ui button" @click="fetchContacts">Recargar contactos</button>
                    </div>
                </div>

                <!-- inline template editor: write the message using the tags from this source -->
                <div v-if="importMode !== 'paste' && detailTags.length" class="ui segment" style="margin-top:12px">
                    <div class="ui small header">Plantilla del mensaje</div>
                    <div style="margin-bottom:6px">
                        Tags disponibles:
                        <a v-for="tag in detailTags" :key="tag" class="ui small teal label" style="cursor:pointer" @click="insertTagDetail(tag)">{{ tag }}</a>
                    </div>
                    <div class="ui form">
                        <div class="field">
                            <textarea rows="3" v-model="selected.campaign.template_body"
                                placeholder="{Hola|Buenas} {nombre}, ..."></textarea>
                        </div>
                        <button class="ui purple button" type="button" :class="{loading: savingTemplate}" @click="saveDetailTemplate">Guardar plantilla</button>
                    </div>
                </div>

                <!-- recipient list -->
                <div class="ui horizontal divider">Recipients (first 100)</div>
                <table class="ui celled compact small table">
                    <thead><tr><th>Lote</th><th>Phone</th><th>Name</th><th>Status</th><th>Sent by</th><th>Error</th></tr></thead>
                    <tbody>
                        <tr v-if="recipients.length === 0"><td colspan="6" style="text-align:center">No recipients yet.</td></tr>
                        <tr v-for="r in recipients" :key="r.id">
                            <td>{{ r.batch }}</td>
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

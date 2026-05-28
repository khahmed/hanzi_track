// HánzìTrack — Alpine component + small helpers.
//
// The single app() factory backs the whole SPA. Views are toggled by the
// `view` field; cross-cutting state (recent, allCategories) is shared so a
// save in the Add view immediately updates the Bank view's pill list.

function app() {
  return {
    view: 'add',

    // Search
    searchQ: '',
    candidates: [],
    searching: false,
    _searchAbort: null,

    // Shared
    recent: [],
    allCategories: [],

    // Save modal (also used for editing existing word categories)
    modal: null,
    modalCategories: [],
    newCategory: '',
    saving: false,
    editWordId: null,  // set when editing an existing word

    // Bank
    bankFilter: '',
    bankResults: [],
    _bankLoaded: false,

    // Dynamic agent tabs. agents[] is populated from /api/agents on init.
    // When an agent tab is active, view === 'agent' and currentAgent holds
    // the row. Slices 2/3 replace the placeholder pane with real content.
    agents: [],
    currentAgent: null,

    // Quizmaster pane state. quiz holds the current question payload from
    // /api/agents/quiz; quizChoice is the user's pick (null = unanswered);
    // quizCorrect is set after submit so the UI can colour the chosen option.
    quizCategory: '',
    quiz: null,
    quizLoading: false,
    quizChoice: null,
    quizCorrect: null,
    quizError: '',
    quizShowPinyin: false,  // NEW: toggle pinyin display

    // Conversationalist pane state. chatMessages is the full thread in
    // chronological order; the assistant rows carry pinyin + coach_notes
    // so each bubble can render its three stacked sections.
    chatMessages: [],
    chatDraft: '',
    chatSending: false,
    chatError: '',
    chatHistoryLoaded: false,

    // Orchestrator pane state. agentStats is loaded from /api/agents/stats
    // when entering an agent tab; orchestrating tracks the POST lifecycle.
    agentStats: [],
    agentStatsLoaded: false,
    orchestrating: false,
    orchResult: null,
    orchError: '',

    // Mutation Cockpit (Loop 2)
    cockpitOpen: false,
    featureDraft: '',
    cockpitLogs: [],
    cockpitRunning: false,

    toast: '',
    _toastTimer: null,

    async init() {
      try {
        await Promise.all([this.loadCategories(), this.loadRecent(), this.loadAgents()]);
      } catch (e) {
        this.flashToast('Failed to load: ' + e.message);
      }
    },

    async loadCategories() {
      const r = await fetch('/api/categories');
      if (!r.ok) throw new Error('categories HTTP ' + r.status);
      this.allCategories = (await r.json()).results || [];
    },

    async loadAgents() {
      const r = await fetch('/api/agents');
      if (!r.ok) throw new Error('agents HTTP ' + r.status);
      this.agents = (await r.json()).results || [];
    },

    selectAgent(agent) {
      this.view = 'agent';
      this.currentAgent = agent;
      this.resetQuiz();
      this.loadAgentStats();
      if (agent.name === 'conversationalist' && !this.chatHistoryLoaded) {
        this.loadChatHistory();
      }
    },

    async loadChatHistory() {
      try {
        const r = await fetch('/api/agents/chat/history');
        if (!r.ok) throw new Error('history HTTP ' + r.status);
        this.chatMessages = (await r.json()).results || [];
        this.chatHistoryLoaded = true;
        this.$nextTick(() => this.scrollChatToEnd());
      } catch (e) {
        this.chatError = e.message;
        console.error(e);
      }
    },

    scrollChatToEnd() {
      const el = document.getElementById('chat-scroll');
      if (el) el.scrollTop = el.scrollHeight;
    },

    async sendChat() {
      const text = this.chatDraft.trim();
      if (!text || this.chatSending) return;
      this.chatSending = true;
      this.chatError = '';
      // Optimistic user bubble — replaced by the server row after the
      // round-trip lands (matched on user_id from the response).
      const optimisticID = 'pending-' + Date.now();
      this.chatMessages.push({ id: optimisticID, role: 'user', content: text });
      this.chatDraft = '';
      this.$nextTick(() => this.scrollChatToEnd());

      try {
        const r = await fetch('/api/agents/chat', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ message: text }),
        });
        if (!r.ok) {
          const msg = await r.text();
          this.chatError = msg || ('chat HTTP ' + r.status);
          // Roll back the optimistic bubble.
          this.chatMessages = this.chatMessages.filter(m => m.id !== optimisticID);
          return;
        }
        const data = await r.json();
        // Replace the optimistic row with the server-canonical one and
        // append the assistant turn.
        const idx = this.chatMessages.findIndex(m => m.id === optimisticID);
        if (idx >= 0) {
          this.chatMessages[idx] = { id: data.user_id, role: 'user', content: text };
        }
        this.chatMessages.push({
          id: data.message_id,
          role: 'assistant',
          content: data.turn.chinese,
          pinyin: data.turn.pinyin,
          coach_notes: data.turn.coach_notes,
        });
        this.$nextTick(() => this.scrollChatToEnd());
      } catch (e) {
        this.chatError = e.message;
        this.chatMessages = this.chatMessages.filter(m => m.id !== optimisticID);
      } finally {
        this.chatSending = false;
      }
    },

    resetQuiz() {
      this.quiz = null;
      this.quizChoice = null;
      this.quizCorrect = null;
      this.quizError = '';
      this.quizShowPinyin = false;  // reset toggle
    },

    async loadAgentStats() {
      this.agentStatsLoaded = false;
      this.orchResult = null;
      this.orchError = '';
      try {
        const r = await fetch('/api/agents/stats');
        if (!r.ok) throw new Error('stats HTTP ' + r.status);
        this.agentStats = (await r.json()).results || [];
      } catch (e) {
        console.error(e);
      } finally {
        this.agentStatsLoaded = true;
      }
    },

    // Returns the stats row for the currently selected agent, or null.
    agentStatsForCurrent() {
      if (!this.currentAgent) return null;
      return this.agentStats.filter(s => s.agent_type === this.currentAgent.name)[0] || null;
    },

    // Color and arrow for the stats badge. Green for >80%, amber between, red for <60%.
    statsBadge(stats) {
      if (!stats || stats.total === 0) return {color: 'text-slate-400', label: '—', arrow: ''};
      const pct = Math.round(stats.pct_correct * 100);
      const arrow = stats.trend === 'improving' ? '↑' : stats.trend === 'declining' ? '↓' : '→';
      const color = pct >= 80 ? 'text-green-600' : pct >= 60 ? 'text-amber-600' : 'text-red-600';
      return {color, label: pct + '%', arrow};
    },

    async runOrchestrate() {
      if (this.orchestrating) return;
      this.orchestrating = true;
      this.orchResult = null;
      this.orchError = '';
      try {
        const r = await fetch('/api/agents/orchestrate', { method: 'POST' });
        if (!r.ok) {
          const msg = await r.text();
          this.orchError = msg || ('orchestrate HTTP ' + r.status);
          return;
        }
        this.orchResult = await r.json();
        // Refresh stats after orchestration.
        await this.loadAgentStats();
        this.flashToast('Prompts tuned — check the results below');
      } catch (e) {
        this.orchError = e.message;
      } finally {
        this.orchestrating = false;
      }
    },

    // Returns the change entry for the current agent, if any.
    orchChangeForCurrent() {
      if (!this.orchResult || !this.currentAgent) return null;
      return this.orchResult.changes.filter(c => c.agent_name === this.currentAgent.name)[0] || null;
    },

    // ---- Mutation Cockpit (Loop 2) ----

    toggleCockpit() {
      this.cockpitOpen = !this.cockpitOpen;
    },
    closeCockpit() {
      this.cockpitOpen = false;
    },

    async submitFeature() {
      const desc = this.featureDraft.trim();
      if (!desc || this.cockpitRunning) return;
      this.cockpitRunning = true;
      this.cockpitLogs = [];
      this.featureDraft = '';
      try {
        const r = await fetch('/api/devops/mutate', {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json',
            'Accept': 'text/event-stream',
          },
          body: JSON.stringify({ feature_description: desc }),
        });
        if (!r.ok) {
          this.cockpitLogs.push({ step: 'error', message: 'Request failed', detail: 'HTTP ' + r.status });
          this.cockpitRunning = false;
          return;
        }
        const reader = r.body.getReader();
        const decoder = new TextDecoder();
        let buffer = '';
        let done = false;
        while (!done) {
          const { value, done: streamDone } = await reader.read();
          done = streamDone;
          buffer += decoder.decode(value || new Uint8Array(), { stream: !done });
          const result = this._parseSSEBuffer(buffer);
          buffer = result.remainder;
          for (const ev of result.parsed) {
            this.cockpitLogs.push(ev);
          }
          this.$nextTick(() => {
            const el = document.getElementById('cockpit-log');
            if (el) el.scrollTop = el.scrollHeight;
          });
        }
      } catch (e) {
        this.cockpitLogs.push({ step: 'error', message: 'Connection error', detail: e.message });
      } finally {
        this.cockpitRunning = false;
      }
    },

    // Parses SSE buffer into parsed events and returns remainder.
    _parseSSEBuffer(buffer) {
      const parsed = [];
      const parts = buffer.split('\n\n');
      const remainder = parts.pop() || '';
      for (const block of parts) {
        if (!block.trim()) continue;
        const lines = block.split('\n');
        let event = 'message';
        let data = '';
        for (const line of lines) {
          if (line.startsWith('event: ')) event = line.slice(7);
          else if (line.startsWith('data: ')) data = line.slice(6);
        }
        if (data) {
          try {
            parsed.push({ step: event, ...JSON.parse(data) });
          } catch (e) {
            parsed.push({ step: event, data });
          }
        }
      }
      return { parsed, remainder };
    },

    // quizQuestionParts splits question_chinese on "____" so the template
    // can render the blank as a stylised slot rather than inline underscores.
    quizQuestionParts() {
      if (!this.quiz) return ['', ''];
      const parts = this.quiz.question_chinese.split('____');
      return [parts[0] || '', parts.slice(1).join('____')];
    },

    // NEW: toggle pinyin display
    toggleQuizPinyin() {
      this.quizShowPinyin = !this.quizShowPinyin;
    },

    async generateQuiz() {
      if (this.quizLoading) return;
      this.resetQuiz();
      this.quizLoading = true;
      try {
        const params = new URLSearchParams();
        if (this.quizCategory) params.set('category', this.quizCategory);
        const r = await fetch('/api/agents/quiz?' + params.toString());
        if (!r.ok) {
          const msg = await r.text();
          this.quizError = msg || ('quiz HTTP ' + r.status);
          return;
        }
        this.quiz = await r.json();
      } catch (e) {
        this.quizError = e.message;
      } finally {
        this.quizLoading = false;
      }
    },

    async pickQuizOption(opt) {
      if (!this.quiz || this.quizChoice !== null) return;
      this.quizChoice = opt;
      const isCorrect = opt === this.quiz.correct_answer;
      this.quizCorrect = isCorrect;
      try {
        await fetch('/api/agents/quiz/submit', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            vocab_id: this.quiz.vocab_id,
            quiz_type: 'structural_fill',
            is_correct: isCorrect,
          }),
        });
      } catch (e) {
        console.error('quiz submit:', e);
      }
    },

    async loadRecent() {
      const r = await fetch('/api/vocab?limit=5');
      if (!r.ok) throw new Error('recent HTTP ' + r.status);
      this.recent = (await r.json()).results || [];
    },

    async runSearch() {
      const q = this.searchQ.trim();
      if (q === '') {
        this.candidates = [];
        this.searching = false;
        return;
      }
      if (this._searchAbort) this._searchAbort.abort();
      this._searchAbort = new AbortController();
      this.searching = true;
      try {
        const r = await fetch('/api/dict/search?q=' + encodeURIComponent(q),
                              { signal: this._searchAbort.signal });
        if (!r.ok) throw new Error('search HTTP ' + r.status);
        this.candidates = (await r.json()).results || [];
      } catch (e) {
        if (e.name !== 'AbortError') {
          this.flashToast('Search failed');
          console.error(e);
        }
      } finally {
        this.searching = false;
      }
    },

    openSaveModal(candidate) {
      // Opens modal for saving a new word from dictionary search.
      this.editWordId = null;
      this.modal = candidate;
      this.modalCategories = [];
      this.newCategory = '';
    },

    openEditModal(word) {
      // Opens modal for editing categories of an existing word from the bank.
      this.editWordId = word.id;
      this.modal = { hanzi: word.hanzi, pinyin: word.pinyin, english: word.english };
      this.modalCategories = [...(word.categories || [])];
      this.newCategory = '';
    },

    closeModal() {
      this.modal = null;
      this.modalCategories = [];
      this.newCategory = '';
      this.editWordId = null;
    },

    toggleCategory(name) {
      const i = this.modalCategories.indexOf(name);
      if (i >= 0) this.modalCategories.splice(i, 1);
      else this.modalCategories.push(name);
    },

    addNewCategory() {
      const name = this.newCategory.trim();
      if (!name) return;
      if (!this.allCategories.includes(name)) this.allCategories.push(name);
      if (!this.modalCategories.includes(name)) this.modalCategories.push(name);
      this.newCategory = '';
    },

    async save() {
      if (!this.modal || this.saving) return;
      this.saving = true;
      try {
        if (this.editWordId) {
          // Update existing word's categories
          const r = await fetch('/api/vocab/' + this.editWordId, {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ categories: this.modalCategories }),
          });
          if (!r.ok) throw new Error('update HTTP ' + r.status);
          this.flashToast('Categories updated');
        } else {
          // Save new word
          const r = await fetch('/api/vocab', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
              hanzi: this.modal.hanzi,
              pinyin: this.modal.pinyin,
              english: this.modal.english,
              categories: this.modalCategories,
            }),
          });
          if (!r.ok) throw new Error('save HTTP ' + r.status);
          const d = await r.json();
          this.flashToast(d.created ? 'Saved to notebook' : 'Already in bank — categories merged');
        }
        this.closeModal();
        this.searchQ = '';
        this.candidates = [];
        await Promise.all([this.loadCategories(), this.loadRecent()]);
        // Refresh bank if we are in bank view
        this._bankLoaded = false;
        if (this.view === 'bank') await this.loadBank();
      } catch (e) {
        this.flashToast('Save failed: ' + e.message);
        console.error(e);
      } finally {
        this.saving = false;
      }
    },

    async renameCategory(oldName) {
      const newName = prompt(`Rename category "${oldName}" to:`, oldName);
      if (!newName || newName === oldName) return;
      try {
        const r = await fetch('/api/categories/rename', {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ old_name: oldName, new_name: newName }),
        });
        if (!r.ok) throw new Error('rename HTTP ' + r.status);
        this.flashToast(`Category renamed to "${newName}"`);
        await Promise.all([this.loadCategories(), this.loadBank()]);
      } catch (e) {
        this.flashToast('Rename failed: ' + e.message);
      }
    },

    async goBank() {
      this.view = 'bank';
      if (!this._bankLoaded) await this.loadBank();
    },

    async loadBank() {
      const params = new URLSearchParams();
      if (this.bankFilter) params.set('category', this.bankFilter);
      try {
        const r = await fetch('/api/vocab?' + params.toString());
        if (!r.ok) throw new Error('bank HTTP ' + r.status);
        this.bankResults = (await r.json()).results || [];
        this._bankLoaded = true;
      } catch (e) {
        this.flashToast('Failed to load bank');
        console.error(e);
      }
    },

    selectFilter(cat) {
      this.bankFilter = cat;
      this.loadBank();
    },

    flashToast(msg) {
      this.toast = msg;
      clearTimeout(this._toastTimer);
      this._toastTimer = setTimeout(() => { this.toast = ''; }, 2500);
    },

    toneMark(numeric) {
      return toneMark(numeric);
    },
  };
}

// toneMark converts CC-CEDICT-style numeric pinyin to combining-mark pinyin.
// "ni3 hao3"  -> "nǐ hǎo"
// "lu:4"      -> "lǜ"
// "peng2 you5" -> "péng you"
// Placement rule: 'a' wins, then 'o', then 'e', otherwise the last vowel.
function toneMark(numeric) {
  if (!numeric) return '';
  const marks = ['', '̄', '́', '̌', '̀', ''];
  return numeric.split(' ').map(syl => {
    const m = syl.match(/^([a-zA-Z:]+)([1-5])?$/);
    if (!m) return syl;
    let letters = m[1].replace(/u:/g, 'ü').replace(/U:/g, 'Ü');
    const tone = m[2] ? parseInt(m[2], 10) : 0;
    if (tone === 0 || tone === 5) return letters.toLowerCase();

    const lower = letters.toLowerCase();
    let idx = -1;
    if (lower.includes('a')) idx = lower.indexOf('a');
    else if (lower.includes('o')) idx = lower.indexOf('o');
    else if (lower.includes('e')) idx = lower.indexOf('e');
    else {
      const vowels = 'aeiouü';
      for (let i = letters.length - 1; i >= 0; i--) {
        if (vowels.includes(lower[i])) { idx = i; break; }
      }
    }
    if (idx < 0) return letters.toLowerCase();
    const out = letters.slice(0, idx + 1) + marks[tone] + letters.slice(idx + 1);
    return out.normalize('NFC').toLowerCase();
  }).join(' ');
}

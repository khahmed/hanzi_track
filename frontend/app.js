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

    // Save modal
    modal: null,
    modalCategories: [],
    newCategory: '',
    saving: false,

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

    // Conversationalist pane state. chatMessages is the full thread in
    // chronological order; the assistant rows carry pinyin + coach_notes
    // so each bubble can render its three stacked sections.
    chatMessages: [],
    chatDraft: '',
    chatSending: false,
    chatError: '',
    chatHistoryLoaded: false,

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
    },

    // quizQuestionParts splits question_chinese on "____" so the template
    // can render the blank as a stylised slot rather than inline underscores.
    quizQuestionParts() {
      if (!this.quiz) return ['', ''];
      const parts = this.quiz.question_chinese.split('____');
      return [parts[0] || '', parts.slice(1).join('____')];
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
      this.modal = candidate;
      this.modalCategories = [];
      this.newCategory = '';
    },
    closeModal() {
      this.modal = null;
      this.modalCategories = [];
      this.newCategory = '';
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
        this.closeModal();
        this.searchQ = '';
        this.candidates = [];
        await Promise.all([this.loadCategories(), this.loadRecent()]);
        // Bank view will refetch next time it's opened.
        this._bankLoaded = false;
      } catch (e) {
        this.flashToast('Save failed');
        console.error(e);
      } finally {
        this.saving = false;
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

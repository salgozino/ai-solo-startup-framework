'use strict';

// State labels mapping A2A wire values to display names.
const STATE_LABELS = {
  'TASK_STATE_WORKING':        'WORKING',
  'TASK_STATE_COMPLETED':      'COMPLETED',
  'TASK_STATE_FAILED':         'FAILED',
  'TASK_STATE_INPUT_REQUIRED': 'INPUT_REQUIRED',
  'TASK_STATE_SUBMITTED':      'SUBMITTED',
  'TASK_STATE_REJECTED':       'REJECTED',
  'TASK_STATE_CANCELED':       'CANCELED',
};

const indicator    = document.getElementById('status-indicator');
const supState     = document.getElementById('supervisor-state');
const taskList     = document.getElementById('task-list');

// Auto-refresh state
var autoRefreshInterval = null;
var autoRefreshEnabled  = false;

function setIndicator(state) {
  indicator.className = 'indicator ' + state;
  indicator.textContent = state === 'live' ? 'Live' : state === 'error' ? 'Disconnected' : 'Connecting…';
}

function displayLabel(state) {
  return STATE_LABELS[state] || state || '—';
}

function taskKey(t) { return t.verdict_id || t.task_id; }

// buildTree groups tasks into nodes {task, children}. A task listed
// as a delegation of another listed task becomes that task's child (delegation
// order, recursively) and is not repeated at top level; a child whose parent
// is not listed stays top-level. A task whose follow_up_of names a listed task
// is nested under it, after its delegations. Top-level order is newest first.
function buildTree(tasks) {
  var byId = {}, byKey = {};
  tasks.forEach(function(t) {
    if (!(t.task_id in byId)) byId[t.task_id] = t;
    if (!(taskKey(t) in byKey)) byKey[taskKey(t)] = t;
  });
  var isChild = {};
  tasks.forEach(function(t) {
    (t.delegations || []).forEach(function(d) {
      var c = byId[d.task_id];
      if (c && c !== t) isChild[d.task_id] = true;
    });
  });
  var followUps = {};
  var isFollowUp = {};
  tasks.forEach(function(t) {
    var p = t.follow_up_of ? byKey[t.follow_up_of] : null;
    if (p && p !== t) {
      (followUps[taskKey(p)] = followUps[taskKey(p)] || []).push(t);
      isFollowUp[taskKey(t)] = true;
    }
  });
  var seen = {};
  function node(t) {
    seen[taskKey(t)] = true;
    var children = [];
    (t.delegations || []).forEach(function(d) {
      var c = byId[d.task_id];
      if (c && c !== t && !seen[taskKey(c)]) children.push(node(c));
    });
    (followUps[taskKey(t)] || []).forEach(function(c) {
      if (!seen[taskKey(c)]) children.push(node(c));
    });
    return { task: t, children: children };
  }
  return tasks.slice().reverse().filter(function(t) {
    return !isChild[t.task_id] && !isFollowUp[taskKey(t)];
  }).map(node);
}

function renderNode(n) {
  var kids = n.children.length
    ? '<div class="task-children">' + n.children.map(renderNode).join('') + '</div>'
    : '';
  return '<div class="task-node">' + renderCard(n.task) + kids + '</div>';
}

function renderCard(t) {
  const key = taskKey(t);
  const label  = displayLabel(t.state);
  const isInput = t.state === 'TASK_STATE_INPUT_REQUIRED' && !t.awaiting_peer;
  const waiting = t.awaiting_peer
    ? `<div class="task-awaiting">Awaiting ${escHtml(t.awaiting_peer)}${t.awaiting_peer_approval ? "'s human approval" : ''}</div>`
    : '';
  const actions = isInput
    ? `<div class="task-actions">
         <button class="btn btn-approve" onclick="verdict('${key}','approve')">Approve</button>
         <button class="btn btn-reject"  onclick="verdict('${key}','reject')">Reject</button>
       </div>`
    : '';
  const followUpOf = t.follow_up_of
    ? `<div class="task-followup-of">Follow-up of ${escHtml(t.follow_up_of)}</div>`
    : '';
  // The server decides eligibility (terminal CEO tasks only).
  const followUp = t.can_follow_up
    ? `<div class="task-followup">
         <button class="btn btn-send" onclick="toggleFollowUp('${key}')">Request changes</button>
         <div class="task-followup-form" style="display:none">
           <textarea class="task-followup-input" rows="3" placeholder="What should change?"></textarea>
           <button class="btn btn-send" onclick="sendFollowUp('${key}')">Send</button>
         </div>
       </div>`
    : '';
  const outputHtml = t.output
    ? `<div class="task-output" id="output-${key}">
         <div class="task-output-header" onclick="toggleOutput('${key}')">
           <span class="task-output-toggle">▶</span> Agent output
         </div>
         <div class="task-output-body"><pre>${escHtml(t.output)}</pre></div>
       </div>`
    : '';
  return `<div class="task-card" id="task-${key}">
    <div class="task-header">
      <span class="task-id">${escHtml(t.agent ? t.agent + ' · ' : '')}${escHtml(t.task_id)}</span>
      <span class="task-state task-state-${label}">${label}</span>
    </div>
    ${followUpOf}
    <div class="task-input">${escHtml(t.input || '')}</div>
    ${waiting}
    ${outputHtml}
    ${actions}
    ${followUp}
  </div>`;
}

function renderTasks(tasks) {
  if (!tasks || tasks.length === 0) {
    taskList.innerHTML = '<p class="empty">No tasks.</p>';
    return;
  }
  // Remember which output toggles are open before re-rendering.
  var openOutputs = {};
  taskList.querySelectorAll('.task-output-body.open').forEach(function(el) {
    var container = el.closest('.task-output');
    if (container && container.id) {
      // id is "output-{taskID}"
      openOutputs[container.id] = true;
    }
  });
  // Same for follow-up forms: keep the open state and typed text.
  var openForms = {};
  taskList.querySelectorAll('.task-followup-form').forEach(function(form) {
    var card = form.closest('.task-card');
    var input = form.querySelector('textarea');
    if (card && card.id && (form.style.display !== 'none' || input.value)) {
      openForms[card.id] = {
        open: form.style.display !== 'none',
        text: input.value,
        focus: document.activeElement === input,
      };
    }
  });
  taskList.innerHTML = buildTree(tasks).map(renderNode).join('');
  Object.keys(openForms).forEach(function(id) {
    var card = document.getElementById(id);
    var form = card && card.querySelector('.task-followup-form');
    if (!form) return;
    var st = openForms[id];
    form.style.display = st.open ? '' : 'none';
    var input = form.querySelector('textarea');
    input.value = st.text;
    if (st.focus) input.focus();
  });
  // Restore open toggles after re-render.
  Object.keys(openOutputs).forEach(function(id) {
    var container = document.getElementById(id);
    if (!container) return;
    var body = container.querySelector('.task-output-body');
    var toggle = container.querySelector('.task-output-toggle');
    if (body) body.classList.add('open');
    if (toggle) toggle.textContent = '▼';
  });
}

function toggleOutput(taskID) {
  var container = document.getElementById('output-' + taskID);
  if (!container) return;
  var header = container.querySelector('.task-output-header');
  var toggle = header.querySelector('.task-output-toggle');
  var body = container.querySelector('.task-output-body');
  var isOpen = body.classList.contains('open');
  if (isOpen) {
    body.classList.remove('open');
    toggle.textContent = '▶';
  } else {
    body.classList.add('open');
    toggle.textContent = '▼';
  }
}

function toggleFollowUp(key) {
  var card = document.getElementById('task-' + key);
  var form = card && card.querySelector('.task-followup-form');
  if (!form) return;
  form.style.display = form.style.display === 'none' ? '' : 'none';
  if (form.style.display !== 'none') form.querySelector('textarea').focus();
}

function sendFollowUp(key) {
  var card = document.getElementById('task-' + key);
  if (!card) return;
  var input = card.querySelector('.task-followup-input');
  var btns = card.querySelectorAll('.task-followup-form .btn');
  var msg = input.value.trim();
  if (!msg) { showToast('Write the requested changes first', 'error'); return; }
  btns.forEach(function(b) { b.disabled = true; });
  fetch('/api/follow-up', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ task_id: key, message: msg }),
  })
    .then(function(r) {
      if (!r.ok) throw new Error('follow-up failed');
      return r.json();
    })
    .then(function() {
      showToast('Follow-up sent', 'success');
      input.value = '';
      card.querySelector('.task-followup-form').style.display = 'none';
      btns.forEach(function(b) { b.disabled = false; });
      setTimeout(fetchTasks, 500);
    })
    .catch(function() {
      showToast('Failed to send follow-up', 'error');
      btns.forEach(function(b) { b.disabled = false; });
    });
}

function escHtml(s) {
  return s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;');
}

function fetchTasks() {
  fetch('/api/tasks')
    .then(function(r) { return r.json(); })
    .then(function(data) {
      renderTasks(data.tasks || []);
    })
    .catch(function() {});
}

function verdict(taskID, action) {
  var card = document.getElementById('task-' + taskID);
  var btn = card ? card.querySelectorAll('.btn') : [];
  btn.forEach(function(b) { b.disabled = true; });
  fetch('/api/' + action, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ task_id: taskID }),
  })
    .then(fetchTasks)
    .catch(function() {
      btn.forEach(function(b) { b.disabled = false; });
    });
}

// SSE — receive live state-change events from the server.
function connectSSE() {
  var es = new EventSource('/api/events');

  es.addEventListener('open', function() {
    setIndicator('live');
  });

  // 'state' events carry JSON with supervisor + tasks snapshot.
  es.addEventListener('state', function(e) {
    try {
      var d = JSON.parse(e.data);
      if (d.supervisor) {
        supState.textContent = d.supervisor;
      }
      if (d.tasks !== undefined) {
        renderTasks(d.tasks);
      }
    } catch (_) {}
  });

  es.addEventListener('error', function() {
    setIndicator('error');
    es.close();
    // Reconnect after 3 s.
    setTimeout(connectSSE, 3000);
  });
}

// Auto-refresh toggle.
var refreshToggle = document.getElementById('auto-refresh-toggle');
refreshToggle.addEventListener('click', function() {
  autoRefreshEnabled = !autoRefreshEnabled;
  refreshToggle.textContent = autoRefreshEnabled ? 'Auto-refresh: ON' : 'Auto-refresh: OFF';
  refreshToggle.classList.toggle('active', autoRefreshEnabled);
  if (autoRefreshEnabled) {
    autoRefreshInterval = setInterval(fetchTasks, 5000);
  } else {
    clearInterval(autoRefreshInterval);
    autoRefreshInterval = null;
  }
});

// Bootstrap.
fetchTasks();
connectSSE();

// Send task to CEO.
var sendBtn    = document.getElementById('send-btn');
var msgInput   = document.getElementById('task-message');

msgInput.addEventListener('input', function() {
  sendBtn.disabled = msgInput.value.trim() === '';
});

sendBtn.addEventListener('click', function() {
  var msg = msgInput.value.trim();
  if (!msg) return;

  sendBtn.disabled = true;
  sendBtn.textContent = 'Sending…';
  fetch('/api/send', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ message: msg }),
  })
    .then(function(r) {
      if (!r.ok) throw new Error('send failed');
      return r.json();
    })
    .then(function() {
      showToast('Task sent successfully', 'success');
      msgInput.value = '';
      sendBtn.textContent = 'Send Task';
      sendBtn.disabled = true; // textarea is empty, disable button
      msgInput.focus();
      // Refresh after a short delay so the background goroutine has time
      // to persist the new task in the store.
      setTimeout(fetchTasks, 500);
    })
    .catch(function() {
      showToast('Failed to send task', 'error');
      sendBtn.textContent = 'Send Task';
      sendBtn.disabled = false;
    });
});

function showToast(msg, type) {
  var toast = document.createElement('div');
  toast.className = 'toast toast-' + type;
  toast.textContent = msg;
  document.body.appendChild(toast);
  setTimeout(function() { toast.remove(); }, 3000);
}

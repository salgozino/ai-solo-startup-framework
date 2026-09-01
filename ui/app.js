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

function setIndicator(state) {
  indicator.className = 'indicator ' + state;
  indicator.textContent = state === 'live' ? 'Live' : state === 'error' ? 'Disconnected' : 'Connecting…';
}

function displayLabel(state) {
  return STATE_LABELS[state] || state || '—';
}

function renderTasks(tasks) {
  if (!tasks || tasks.length === 0) {
    taskList.innerHTML = '<p class="empty">No tasks.</p>';
    return;
  }
  // Reverse so newest tasks appear first.
  taskList.innerHTML = tasks.slice().reverse().map(function(t) {
    const label  = displayLabel(t.state);
    const isInput = t.state === 'TASK_STATE_INPUT_REQUIRED';
    const actions = isInput
      ? `<div class="task-actions">
           <button class="btn btn-approve" onclick="verdict('${t.task_id}','approve')">Approve</button>
           <button class="btn btn-reject"  onclick="verdict('${t.task_id}','reject')">Reject</button>
         </div>`
      : '';
    return `<div class="task-card" id="task-${t.task_id}">
      <div class="task-header">
        <span class="task-id">${t.task_id}</span>
        <span class="task-state task-state-${label}">${label}</span>
      </div>
      <div class="task-input">${escHtml(t.input || '')}</div>
      ${actions}
    </div>`;
  }).join('');
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
  var btn = document.querySelectorAll('#task-' + taskID + ' .btn');
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

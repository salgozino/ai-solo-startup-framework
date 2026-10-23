import { test, expect, Page } from '@playwright/test';

test.beforeEach(async ({ page, request }) => {
  const res = await request.post('/test/reset');
  expect(res.ok()).toBeTruthy();
  await page.goto('/');
  await expect(page.locator('#status-indicator')).toHaveText('Live');
  await expect(card(page, 'ceo-done')).toBeVisible();
});

// A task card by its task id (the card id is "task-<key>").
const card = (page: Page, id: string) => page.locator(`[id="task-${id}"]`);

test('sending a task lists it', async ({ page }) => {
  await page.getByPlaceholder('Describe the task...').fill('Plan the hiring roadmap');
  await page.getByRole('button', { name: 'Send Task' }).click();
  await expect(page.getByText('Task sent successfully')).toBeVisible();
  await expect(page.locator('.task-input', { hasText: 'Plan the hiring roadmap' })).toBeVisible();
});

test('approving an INPUT_REQUIRED card removes its buttons and changes state', async ({ page }) => {
  const c = card(page, 'ceo-input');
  await expect(c.locator('.task-state')).toHaveText('INPUT_REQUIRED');
  await c.getByRole('button', { name: 'Approve' }).click();
  await expect(c.locator('.task-state')).toHaveText('WORKING');
  await expect(c.getByRole('button', { name: 'Approve' })).toHaveCount(0);
  await expect(c.getByRole('button', { name: 'Reject' })).toHaveCount(0);
});

test('rejecting an INPUT_REQUIRED card removes its buttons and changes state', async ({ page }) => {
  const c = card(page, 'ceo-input');
  await c.getByRole('button', { name: 'Reject' }).click();
  await expect(c.locator('.task-state')).toHaveText('REJECTED');
  await expect(c.getByRole('button', { name: 'Approve' })).toHaveCount(0);
  await expect(c.getByRole('button', { name: 'Reject' })).toHaveCount(0);
});

test('a delegated task is nested under its parent, not listed at top level', async ({ page }) => {
  const parent = page.locator('.task-node', { has: card(page, 'ceo-delegating') }).first();
  await expect(parent.locator('.task-children [id="task-eng-done"]')).toBeVisible();
  await expect(page.locator('#task-list > .task-node [id="task-eng-done"]')).toHaveCount(1);
  await expect(page.locator('#task-list > .task-node > [id="task-eng-done"]')).toHaveCount(0);
});

test('"Request changes" is shown exactly on can_follow_up cards', async ({ page }) => {
  await expect(card(page, 'ceo-done').getByRole('button', { name: 'Request changes' })).toBeVisible();
  for (const id of ['ceo-input', 'ceo-delegating', 'ceo-working', 'eng-done']) {
    await expect(card(page, id)).toBeVisible();
    await expect(card(page, id).getByRole('button', { name: 'Request changes' })).toHaveCount(0);
  }
  await expect(page.getByRole('button', { name: 'Request changes' })).toHaveCount(1);
});

test('a submitted follow-up shows a success toast and nests under its task', async ({ page }) => {
  const c = card(page, 'ceo-done');
  await c.getByRole('button', { name: 'Request changes' }).click();
  await c.getByPlaceholder('What should change?').fill('Make it shorter');
  await c.getByRole('button', { name: 'Send', exact: true }).click();
  await expect(page.getByText('Follow-up sent')).toBeVisible();

  const node = page.locator('.task-node', { has: c }).first();
  const child = node.locator('.task-children .task-card', { hasText: 'Make it shorter' });
  await expect(child).toBeVisible();
  await expect(child.getByText('Follow-up of ceo-done')).toBeVisible();
});

test('a refused follow-up shows a failure toast and creates no card', async ({ page, request }) => {
  const c = card(page, 'ceo-done');
  await c.getByRole('button', { name: 'Request changes' }).click();
  await c.getByPlaceholder('What should change?').fill('Too late');
  // The task becomes non-terminal server-side while the browser still shows the old view.
  const res = await request.post('/test/set-state?task=ceo-done&state=TASK_STATE_WORKING');
  expect(res.ok()).toBeTruthy();
  await c.getByRole('button', { name: 'Send', exact: true }).click();
  await expect(page.getByText('Failed to send follow-up')).toBeVisible();
  await expect(page.locator('.task-input', { hasText: 'Too late' })).toHaveCount(0);
});

test('typed follow-up text survives an SSE re-render', async ({ page, request }) => {
  const c = card(page, 'ceo-done');
  await c.getByRole('button', { name: 'Request changes' }).click();
  await c.getByPlaceholder('What should change?').fill('Keep this draft');

  // Wait for the re-render itself: the old card element is replaced.
  const handle = await c.elementHandle();
  const res = await request.post('/test/broadcast');
  expect(res.ok()).toBeTruthy();
  await page.waitForFunction((el) => el && !el.isConnected, handle);

  await expect(card(page, 'ceo-done').getByPlaceholder('What should change?')).toHaveValue('Keep this draft');
  await expect(card(page, 'ceo-done').getByPlaceholder('What should change?')).toBeVisible();
});

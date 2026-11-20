package ui

// themeCSS is intentionally small: colors, spacing, typography, borders, and
// focus states. There are no gradients, shadows, glows, or decorative motion.
const themeCSS = `
window,
window.background {
  background-color: #0d1117;
  color: #e6edf3;
}

headerbar {
  background-color: #111820;
  background-image: none;
  border-bottom: 1px solid #202932;
  min-height: 46px;
  padding: 0 10px;
}

headerbar .title {
  font-weight: 600;
  font-size: 13px;
}

.product-name {
  font-weight: 600;
  font-size: 13px;
  color: #e6edf3;
}

.product-sub {
  font-size: 11px;
  color: #687582;
}

.nav-rail {
  background-color: #0b0f14;
  border-right: 1px solid #202932;
  padding: 10px 8px;
}

.nav-section {
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.09em;
  color: #687582;
  margin: 8px 10px 4px 10px;
}

row.nav-row {
  border-radius: 5px;
  border-left: 2px solid transparent;
  padding: 0;
  margin: 1px 0;
}

row.nav-row:hover {
  background-color: #161d25;
}

row.nav-row:selected {
  background-color: #1b242e;
  border-left-color: #8bcbb8;
}

.nav-item {
  padding: 7px 9px;
  font-size: 13px;
  color: #9aa6b2;
}

row.nav-row:selected .nav-item {
  color: #e6edf3;
}

.nav-icon {
  color: #687582;
}

row.nav-row:selected .nav-icon {
  color: #8bcbb8;
}

.page {
  background-color: #0d1117;
}

.page-title {
  font-size: 18px;
  font-weight: 600;
  color: #e6edf3;
}

.page-subtitle {
  font-size: 12px;
  color: #9aa6b2;
}

.section-title {
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.09em;
  color: #687582;
  margin-top: 18px;
  margin-bottom: 2px;
}

.field-label {
  font-size: 12px;
  color: #9aa6b2;
}

.field-value {
  font-size: 12px;
  color: #e6edf3;
}

.field-muted {
  color: #687582;
}

.mono {
  font-family: "DejaVu Sans Mono", "Liberation Mono", monospace;
  font-size: 12px;
}

.muted {
  color: #687582;
}

.secondary {
  color: #9aa6b2;
}

.accent {
  color: #8bcbb8;
}

.success {
  color: #82b99a;
}

.warning {
  color: #d5b575;
}

.error-text {
  color: #d98282;
}

.status-label {
  font-size: 12px;
  color: #9aa6b2;
}

.status-icon-disconnected {
  color: #687582;
}

.status-icon-active {
  color: #8bcbb8;
}

.status-icon-warning {
  color: #d5b575;
}

.status-icon-error {
  color: #d98282;
}

.inline-error {
  background-color: #1a1214;
  border: 1px solid #4a2f33;
  border-radius: 4px;
  padding: 8px 10px;
}

.notice {
  background-color: #111820;
  border: 1px solid #28323c;
  border-radius: 4px;
  padding: 8px 10px;
}

.separator {
  background-color: #202932;
  min-height: 1px;
}

button {
  background-color: #161d25;
  background-image: none;
  border: 1px solid #28323c;
  border-radius: 5px;
  color: #e6edf3;
  min-height: 28px;
  padding: 3px 10px;
  box-shadow: none;
}

button:hover {
  background-color: #1b242e;
}

button:active {
  background-color: #202932;
}

button:disabled {
  color: #687582;
  background-color: #111820;
  border-color: #202932;
}

button.primary {
  background-color: #8bcbb8;
  border-color: #8bcbb8;
  color: #0d1117;
  font-weight: 600;
}

button.primary:hover {
  background-color: #9ad6c4;
}

button.primary:disabled {
  background-color: #28323c;
  border-color: #28323c;
  color: #9aa6b2;
}

button.danger {
  color: #d98282;
  border-color: #4a2f33;
}

button.danger:hover {
  background-color: #24191b;
}

entry,
searchentry {
  background-color: #0d1117;
  background-image: none;
  border: 1px solid #28323c;
  border-radius: 4px;
  color: #e6edf3;
  min-height: 30px;
  padding: 2px 8px;
}

entry:focus,
searchentry:focus {
  border-color: #8bcbb8;
}

entry selection {
  background-color: #2f4f47;
  color: #e6edf3;
}

list {
  background-color: transparent;
}

list > row {
  background-color: transparent;
  border-radius: 4px;
}

list > row:hover {
  background-color: #161d25;
}

list > row:selected {
  background-color: #1b242e;
}

.detail-pane {
  background-color: #111820;
  border-left: 1px solid #202932;
  padding: 16px 18px;
}

.list-pane {
  background-color: #0d1117;
}

.empty-title {
  font-size: 13px;
  color: #9aa6b2;
}

.empty-detail {
  font-size: 12px;
  color: #687582;
}

switch {
  background-color: #161d25;
  border: 1px solid #28323c;
  border-radius: 10px;
  min-width: 38px;
  min-height: 20px;
}

switch:checked {
  background-color: #8bcbb8;
  border-color: #8bcbb8;
}

switch > slider {
  background-color: #9aa6b2;
  border-radius: 8px;
  min-width: 16px;
  min-height: 16px;
}

switch:checked > slider {
  background-color: #0d1117;
}

spinbutton {
  background-color: #0d1117;
  border: 1px solid #28323c;
  border-radius: 4px;
  color: #e6edf3;
}

dropdown {
  background-color: #0d1117;
  border: 1px solid #28323c;
  border-radius: 4px;
  color: #e6edf3;
}

progressbar trough {
  background-color: #161d25;
  border: 1px solid #202932;
  border-radius: 3px;
  min-height: 5px;
}

progressbar progress {
  background-color: #8bcbb8;
  border-radius: 3px;
  min-height: 5px;
}

scrolledwindow {
  border: none;
}

scrollbar {
  background-color: transparent;
}

scrollbar slider {
  background-color: #28323c;
  border-radius: 3px;
  min-width: 6px;
  min-height: 6px;
}

tooltip {
  background-color: #161d25;
  color: #e6edf3;
  border: 1px solid #28323c;
  border-radius: 4px;
  padding: 5px 7px;
}

.dialog-body {
  padding: 16px;
}

.dialog-title {
  font-size: 15px;
  font-weight: 600;
}

.expander-title {
  font-size: 11px;
  color: #9aa6b2;
}

textview {
  background-color: #0d1117;
  color: #9aa6b2;
  border: 1px solid #202932;
  border-radius: 4px;
}

*:focus-visible {
  outline: 1px solid #8bcbb8;
  outline-offset: -1px;
}

label.dim {
  color: #687582;
}

`

/**
 * @author: 公众号：IT杨秀才
 * @doc:Student-Coach - Adaptive Learning and Knowledge Mastery Diagnosis
 */

import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import App from './App'
import './index.css'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)

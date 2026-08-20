import { useCallback, useEffect, useRef, useState, type PointerEvent as ReactPointerEvent, type ReactNode } from 'react'
import './ResizableRightPanel.css'

const MIN_WIDTH=320
const MAX_WIDTH=760
const GRID_WIDTH=360

function availableMaxWidth(){
  if(typeof window==='undefined')return MAX_WIDTH
  return Math.max(MIN_WIDTH,Math.min(MAX_WIDTH,window.innerWidth-GRID_WIDTH))
}
function clampWidth(width:number){return Math.min(availableMaxWidth(),Math.max(MIN_WIDTH,Math.round(width)))}

export function ResizableRightPanel({panelKey,defaultWidth=400,children}:{panelKey:string;defaultWidth?:number;children:ReactNode}){
  const storageKey=`kanpic:right-panel-width:${panelKey}`
  const [width,setWidth]=useState(()=>{
    const saved=typeof window==='undefined'?null:window.localStorage.getItem(storageKey)
    const stored=saved===null?Number.NaN:Number(saved)
    return clampWidth(Number.isFinite(stored)?stored:defaultWidth)
  })
  const drag=useRef<{x:number;width:number}|undefined>(undefined)
  const updateWidth=useCallback((next:number)=>setWidth(clampWidth(next)),[])

  useEffect(()=>{window.localStorage.setItem(storageKey,String(width))},[storageKey,width])
  useEffect(()=>()=>document.body.classList.remove('right-panel-resizing'),[])
  useEffect(()=>{
    const resize=()=>updateWidth(width)
    window.addEventListener('resize',resize)
    return()=>window.removeEventListener('resize',resize)
  },[updateWidth,width])

  const startResize=(event:ReactPointerEvent<HTMLDivElement>)=>{
    drag.current={x:event.clientX,width}
    event.currentTarget.setPointerCapture(event.pointerId)
    document.body.classList.add('right-panel-resizing')
  }
  const moveResize=(event:ReactPointerEvent<HTMLDivElement>)=>{
    if(!drag.current)return
    updateWidth(drag.current.width+drag.current.x-event.clientX)
  }
  const stopResize=(event:ReactPointerEvent<HTMLDivElement>)=>{
    drag.current=undefined
    if(event.currentTarget.hasPointerCapture(event.pointerId))event.currentTarget.releasePointerCapture(event.pointerId)
    document.body.classList.remove('right-panel-resizing')
  }
  const reset=()=>updateWidth(defaultWidth)

  return <section className="right-panel-frame" style={{width}} data-panel-key={panelKey}>
    <div
      className="right-panel-resizer"
      role="separator"
      aria-label="우측 패널 너비 조절"
      aria-orientation="vertical"
      aria-valuemin={MIN_WIDTH}
      aria-valuemax={availableMaxWidth()}
      aria-valuenow={width}
      tabIndex={0}
      title="드래그하거나 화살표 키로 패널 너비 조절 · 두 번 클릭해 초기화"
      onPointerDown={startResize}
      onPointerMove={moveResize}
      onPointerUp={stopResize}
      onPointerCancel={stopResize}
      onDoubleClick={reset}
      onKeyDown={event=>{
        if(event.key==='ArrowLeft'){event.preventDefault();updateWidth(width+16)}
        else if(event.key==='ArrowRight'){event.preventDefault();updateWidth(width-16)}
        else if(event.key==='Home'){event.preventDefault();updateWidth(MIN_WIDTH)}
        else if(event.key==='End'){event.preventDefault();updateWidth(availableMaxWidth())}
      }}
    ><span/></div>
    <div className="right-panel-content">{children}</div>
  </section>
}

import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { ChevronRight } from 'lucide-react'
import './ContextMenu.css'

export type MenuItem =
  | {kind:'separator'}
  | {kind:'label';label:string}
  | {kind:'item';label:string;shortcut?:string;icon?:ReactNode;disabled?:boolean;danger?:boolean;checked?:boolean;onSelect:()=>void}
  | {kind:'submenu';label:string;icon?:ReactNode;disabled?:boolean;items:MenuItem[]}

const MENU_WIDTH=252,MENU_MARGIN=8
export function selectableItems(items:MenuItem[]){return items.map((item,index)=>({item,index})).filter(entry=>(entry.item.kind==='item'||entry.item.kind==='submenu')&&!entry.item.disabled)}

/** Positions a menu inside the viewport, flipping it when it would overflow. */
function clampPosition(x:number,y:number,height:number){
  const left=Math.max(MENU_MARGIN,Math.min(x,window.innerWidth-MENU_WIDTH-MENU_MARGIN))
  const top=Math.max(MENU_MARGIN,Math.min(y,window.innerHeight-height-MENU_MARGIN))
  return{left,top}
}

function MenuList({items,onClose,autoFocus}:{items:MenuItem[];onClose:()=>void;autoFocus:boolean}){
  const [active,setActive]=useState(()=>selectableItems(items)[0]?.index??-1),[openSubmenu,setOpenSubmenu]=useState<number>()
  const list=useRef<HTMLDivElement>(null)
  useEffect(()=>{if(autoFocus)list.current?.focus()},[autoFocus])
  const movable=selectableItems(items)
  const move=(direction:1|-1)=>{
    if(movable.length===0)return
    const position=movable.findIndex(entry=>entry.index===active)
    const next=movable[(position+direction+movable.length*2)%movable.length]
    setActive(next.index);setOpenSubmenu(undefined)
  }
  const choose=(item:MenuItem,index:number)=>{
    if(item.kind==='submenu'){setOpenSubmenu(current=>current===index?undefined:index);setActive(index);return}
    if(item.kind!=='item'||item.disabled)return
    onClose();item.onSelect()
  }
  return <div className="context-menu-list" ref={list} tabIndex={-1} role="menu" onKeyDown={event=>{
    if(event.key==='ArrowDown'){move(1);event.preventDefault()}
    else if(event.key==='ArrowUp'){move(-1);event.preventDefault()}
    else if(event.key==='Home'){setActive(movable[0]?.index??-1);event.preventDefault()}
    else if(event.key==='End'){setActive(movable.at(-1)?.index??-1);event.preventDefault()}
    else if(event.key==='ArrowRight'&&items[active]?.kind==='submenu'){setOpenSubmenu(active);event.preventDefault()}
    else if(event.key==='ArrowLeft'&&openSubmenu!==undefined){setOpenSubmenu(undefined);event.preventDefault()}
    else if(event.key==='Enter'||event.key===' '){const item=items[active];if(item)choose(item,active);event.preventDefault()}
    else if(event.key==='Escape'){onClose();event.preventDefault()}
  }}>
    {items.map((item,index)=>{
      if(item.kind==='separator')return <hr key={index}/>
      if(item.kind==='label')return <strong key={index} className="context-menu-label">{item.label}</strong>
      const submenu=item.kind==='submenu'
      return <div key={index} className={`context-menu-row${submenu&&openSubmenu===index?' open':''}`}>
        <button
          role={submenu?'menuitem':item.checked===undefined?'menuitem':'menuitemcheckbox'}
          aria-haspopup={submenu||undefined} aria-expanded={submenu?openSubmenu===index:undefined}
          aria-checked={!submenu&&item.kind==='item'&&item.checked!==undefined?item.checked:undefined}
          className={`${index===active?'active':''}${!submenu&&item.kind==='item'&&item.danger?' danger':''}`}
          disabled={item.disabled} onMouseEnter={()=>{setActive(index);if(!submenu)setOpenSubmenu(undefined)}}
          onClick={()=>choose(item,index)}>
          <span className="context-menu-icon">{item.kind==='item'&&item.checked?'✓':item.icon}</span>
          <span className="context-menu-text">{item.label}</span>
          {submenu?<ChevronRight className="context-menu-arrow"/>:item.kind==='item'&&item.shortcut?<kbd>{item.shortcut}</kbd>:null}
        </button>
        {submenu&&openSubmenu===index&&<div className="context-submenu"><MenuList items={item.items} onClose={onClose} autoFocus={false}/></div>}
      </div>
    })}
  </div>
}

/** A pointer- and keyboard-driven menu rendered at viewport coordinates. */
export function ContextMenu({x,y,items,label,onClose}:{x:number;y:number;items:MenuItem[];label:string;onClose:()=>void}){
  const container=useRef<HTMLDivElement>(null),[position,setPosition]=useState(()=>clampPosition(x,y,240))
  useLayoutEffect(()=>{setPosition(clampPosition(x,y,container.current?.offsetHeight??240))},[x,y,items])
  useEffect(()=>{
    const dismiss=(event:MouseEvent)=>{if(!container.current?.contains(event.target as Node))onClose()}
    const close=()=>onClose()
    window.addEventListener('mousedown',dismiss)
    window.addEventListener('resize',close)
    window.addEventListener('blur',close)
    return()=>{window.removeEventListener('mousedown',dismiss);window.removeEventListener('resize',close);window.removeEventListener('blur',close)}
  },[onClose])
  return <div className="context-menu" ref={container} style={{left:position.left,top:position.top}} aria-label={label} onContextMenu={event=>event.preventDefault()}>
    <MenuList items={items} onClose={onClose} autoFocus/>
  </div>
}

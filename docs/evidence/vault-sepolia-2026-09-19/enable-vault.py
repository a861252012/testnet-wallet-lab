import pathlib,subprocess,json,os,fcntl
p=pathlib.Path('/opt/testnet-wallet-lab');os.chdir(p)
lock=open('deploy.lock','a');fcntl.flock(lock,fcntl.LOCK_EX)
address='0xDbB49ee9eC6ab924eA2D3e37Fd594b06648247bf';rev='3a55ab854701074f6fb796ba23fe7953b67fbd8e'
image=(p/'current-image').read_text().strip()
assert image=='ghcr.io/a861252012/testnet-wallet-lab@sha256:37ca634d51b67c79445f73d7d5a8109bda569cddc1bbbb6a37ffd0a4b222ec18'
c=json.loads(subprocess.check_output(['docker','inspect','testnet-wallet-demo-app-1']))[0]
assert c['Config']['Labels']['org.opencontainers.image.revision']==rev
files=[p/'.env',p/'compose.demo.yaml'];old=[x.read_bytes() for x in files]
env=dict(os.environ,APP_IMAGE=image)
cmd=['docker','compose','--env-file',str(files[0]),'-f',str(files[1])]
def run(args):subprocess.run(args,env=env,check=True)
try:
 lines=old[0].decode().splitlines();lines=[x for x in lines if not x.startswith('SEPOLIA_VAULT_ADDRESS=')];lines.append('SEPOLIA_VAULT_ADDRESS='+address)
 files[0].write_text('\n'.join(lines)+'\n');os.chmod(files[0],0o600)
 compose=old[1].decode();assert 'SEPOLIA_VAULT_ADDRESS' not in compose
 compose=compose.replace('    environment:\n','    environment:\n      SEPOLIA_VAULT_ADDRESS: ${SEPOLIA_VAULT_ADDRESS:-}\n',1);files[1].write_text(compose)
 run(cmd+['config','--quiet']);run(cmd+['stop','app']);run(cmd+['up','-d','--no-build','--pull','never','--wait','--wait-timeout','90'])
 run(['python3',str(p/'verify-demo.py'),'testnet-wallet-demo-app-1',rev])
 c=json.loads(subprocess.check_output(['docker','inspect','testnet-wallet-demo-app-1']))[0]
 actual=dict(x.split('=',1) for x in c['Config']['Env']);assert actual['SEPOLIA_VAULT_ADDRESS']==address
 print(json.dumps({'revision':rev,'image':image,'SEPOLIA_VAULT_ADDRESS':actual['SEPOLIA_VAULT_ADDRESS'],'mounts':[{'source':m['Source'],'destination':m['Destination']} for m in c['Mounts']],'state':c['State']['Status']},indent=2))
except BaseException:
 for file,data in zip(files,old):file.write_bytes(data)
 run(cmd+['stop','app']);run(cmd+['up','-d','--no-build','--pull','never','--wait','--wait-timeout','90']);raise

kubectl patch pod $1 -p '{"metadata":{"finalizers":null}}'
kubectl delete pod $1 --grace-period=0 --force

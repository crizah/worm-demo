worm demo app 

- have the seed script be run automatically at startup
- throttle the traffic slider

things to install
fastfetch
git
docker
build-essentials
make 
go
psql


sudo docker compose exec postgres psql -U postgres -d worm_demo -c "select count(*) from tasks;"
